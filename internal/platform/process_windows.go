//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type ClaudeTask struct {
	PID       uint32
	PIDs      []uint32
	CreatedAt int64
	Detached  bool
	Name      string
	Detail    string
	Bytes     uint64
}

type procEntry struct {
	pid  uint32
	ppid uint32
	exe  string
}

var (
	modPsapi               = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemInfo  = modPsapi.NewProc("GetProcessMemoryInfo")
)

type processMemoryCounters struct {
	Cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

func processMemory(pid uint32) uint64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		h, err = windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if err != nil {
			return 0
		}
	}
	defer windows.CloseHandle(h)
	var pmc processMemoryCounters
	pmc.Cb = uint32(unsafe.Sizeof(pmc))
	r, _, _ := procGetProcessMemInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.Cb))
	if r == 0 {
		return 0
	}
	return uint64(pmc.WorkingSetSize)
}

func processCreationTime(pid uint32) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	return creation.Nanoseconds()
}

func snapshotProcesses() []procEntry {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return nil
	}

	var entries []procEntry
	for {
		entries = append(entries, procEntry{
			pid:  entry.ProcessID,
			ppid: entry.ParentProcessID,
			exe:  windows.UTF16ToString(entry.ExeFile[:]),
		})
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}
	return entries
}

func collectDescendantPIDs(roots []uint32, entries []procEntry) map[uint32]bool {
	childrenByParent := make(map[uint32][]uint32, len(entries))
	for _, e := range entries {
		if e.pid != 0 && e.ppid != e.pid {
			childrenByParent[e.ppid] = append(childrenByParent[e.ppid], e.pid)
		}
	}
	visited := make(map[uint32]bool, len(roots))
	queue := make([]uint32, 0, len(roots))
	for _, r := range roots {
		if r != 0 && !visited[r] {
			visited[r] = true
			queue = append(queue, r)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, ch := range childrenByParent[cur] {
			if !visited[ch] {
				visited[ch] = true
				queue = append(queue, ch)
			}
		}
	}
	return visited
}

func processImagePath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 512)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// ListClaudeProcesses finds running Claude Code CLI and Claude Desktop processes.
func ListClaudeProcesses() []ClaudeTask {
	entries := snapshotProcesses()
	var tasks []ClaudeTask
	for _, e := range entries {
		lower := strings.ToLower(e.exe)
		if lower == "claude.exe" {
			imgPath := processImagePath(e.pid)
			lowerPath := strings.ToLower(imgPath)
			name := "Claude Code"
			detail := fmt.Sprintf("PID %d", e.pid)
			if strings.Contains(lowerPath, "anthropicclaude") || strings.Contains(lowerPath, "claude-desktop") || strings.Contains(lowerPath, `\claude\`) {
				name = "Claude Desktop"
			}
			if imgPath != "" {
				parent := filepath.Base(filepath.Dir(imgPath))
				if parent != "" && parent != "." {
					detail = parent
				}
			}
			tasks = append(tasks, ClaudeTask{
				PID:       e.pid,
				PIDs:      []uint32{e.pid},
				CreatedAt: processCreationTime(e.pid),
				Name:      name,
				Detail:    detail,
				Bytes:     processMemory(e.pid),
			})
		}
	}

	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Bytes != tasks[j].Bytes {
			return tasks[i].Bytes > tasks[j].Bytes
		}
		return tasks[i].PID < tasks[j].PID
	})
	return tasks
}

// ListAntigravityProcesses finds running Antigravity Desktop (grouped), Language Server, and agy CLI processes,
// excluding ccmonitor's own background sync descendant tree.
func ListAntigravityProcesses() []ClaudeTask {
	entries := snapshotProcesses()
	if len(entries) == 0 {
		return nil
	}

	selfPID := uint32(os.Getpid())
	selfTree := collectDescendantPIDs([]uint32{selfPID}, entries)
	alivePIDs := make(map[uint32]bool, len(entries))
	for _, e := range entries {
		alivePIDs[e.pid] = true
	}

	var tasks []ClaudeTask
	var desktopPID uint32
	var desktopPIDs []uint32
	var desktopBytes uint64

	for _, e := range entries {
		pid := e.pid
		if selfTree[pid] {
			continue
		}
		lower := strings.ToLower(e.exe)
		switch lower {
		case "antigravity.exe":
			if desktopPID == 0 || pid < desktopPID {
				desktopPID = pid
			}
			desktopPIDs = append(desktopPIDs, pid)
			desktopBytes += processMemory(pid)
		case "language_server.exe":
			imgPath := strings.ToLower(processImagePath(pid))
			if strings.Contains(imgPath, "antigravity") {
				tasks = append(tasks, ClaudeTask{
					PID:       pid,
					PIDs:      []uint32{pid},
					CreatedAt: processCreationTime(pid),
					Name:      "Language Server",
					Detail:    fmt.Sprintf("PID %d", pid),
					Bytes:     processMemory(pid),
				})
			}
		case "agy.exe":
			tasks = append(tasks, ClaudeTask{
				PID:       pid,
				PIDs:      []uint32{pid},
				CreatedAt: processCreationTime(pid),
				Detached:  !alivePIDs[e.ppid],
				Name:      "agy CLI",
				Detail:    fmt.Sprintf("PID %d", pid),
				Bytes:     processMemory(pid),
			})
		}
	}

	if len(desktopPIDs) > 0 {
		detail := "Desktop"
		if len(desktopPIDs) > 1 {
			detail = fmt.Sprintf("%d procs", len(desktopPIDs))
		}
		tasks = append(tasks, ClaudeTask{
			PID:       desktopPID,
			PIDs:      desktopPIDs,
			CreatedAt: processCreationTime(desktopPID),
			Name:      "Antigravity",
			Detail:    detail,
			Bytes:     desktopBytes,
		})
	}

	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Bytes != tasks[j].Bytes {
			return tasks[i].Bytes > tasks[j].Bytes
		}
		return tasks[i].PID < tasks[j].PID
	})
	return tasks
}

func ForceQuitProcess(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

// ForceQuitTask terminates a task's PIDs and descendant tree after guarding against PID reuse.
func ForceQuitTask(task ClaudeTask) error {
	if task.PID == 0 && len(task.PIDs) == 0 {
		return nil
	}
	if task.PID != 0 && task.CreatedAt != 0 {
		curCreated := processCreationTime(task.PID)
		if curCreated == 0 || curCreated != task.CreatedAt {
			return nil
		}
	}
	roots := append([]uint32(nil), task.PIDs...)
	if task.PID != 0 {
		roots = append(roots, task.PID)
	}
	entries := snapshotProcesses()
	selfTree := collectDescendantPIDs([]uint32{uint32(os.Getpid())}, entries)
	targets := collectDescendantPIDs(roots, entries)

	var firstErr error
	for pid := range targets {
		if selfTree[pid] {
			continue
		}
		if err := ForceQuitProcess(pid); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func FormatBytes(b uint64) string {
	const (
		kb = 1024.0
		mb = 1024.0 * 1024.0
		gb = 1024.0 * 1024.0 * 1024.0
	)
	v := float64(b)
	switch {
	case v >= gb:
		return fmt.Sprintf("%.1f GB", v/gb)
	case v >= mb:
		return fmt.Sprintf("%.0f MB", v/mb)
	case v >= kb:
		return fmt.Sprintf("%.0f KB", v/kb)
	default:
		return fmt.Sprintf("%d B", b)
	}
}
