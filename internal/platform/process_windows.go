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
	PID    uint32
	Name   string
	Detail string
	Bytes  uint64
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

	var tasks []ClaudeTask
	for {
		exe := windows.UTF16ToString(entry.ExeFile[:])
		lower := strings.ToLower(exe)
		if lower == "claude.exe" {
			imgPath := processImagePath(entry.ProcessID)
			lowerPath := strings.ToLower(imgPath)
			name := "Claude Code"
			detail := fmt.Sprintf("PID %d", entry.ProcessID)
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
				PID:    entry.ProcessID,
				Name:   name,
				Detail: detail,
				Bytes:  processMemory(entry.ProcessID),
			})
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
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

// ListAntigravityProcesses finds running Antigravity Desktop (grouped), Language Server, and agy CLI processes.
func ListAntigravityProcesses() []ClaudeTask {
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

	selfPID := uint32(os.Getpid())
	var tasks []ClaudeTask
	var desktopPID uint32
	var desktopCount int
	var desktopBytes uint64

	for {
		pid := entry.ProcessID
		if pid != selfPID {
			exe := windows.UTF16ToString(entry.ExeFile[:])
			lower := strings.ToLower(exe)
			switch lower {
			case "antigravity.exe":
				if desktopPID == 0 || pid < desktopPID {
					desktopPID = pid
				}
				desktopCount++
				desktopBytes += processMemory(pid)
			case "language_server.exe":
				imgPath := strings.ToLower(processImagePath(pid))
				if strings.Contains(imgPath, "antigravity") {
					tasks = append(tasks, ClaudeTask{
						PID:    pid,
						Name:   "Language Server",
						Detail: fmt.Sprintf("PID %d", pid),
						Bytes:  processMemory(pid),
					})
				}
			case "agy.exe":
				tasks = append(tasks, ClaudeTask{
					PID:    pid,
					Name:   "agy CLI",
					Detail: fmt.Sprintf("PID %d", pid),
					Bytes:  processMemory(pid),
				})
			}
		}
		if err := windows.Process32Next(snap, &entry); err != nil {
			break
		}
	}

	if desktopCount > 0 {
		detail := "Desktop"
		if desktopCount > 1 {
			detail = fmt.Sprintf("%d procs", desktopCount)
		}
		tasks = append(tasks, ClaudeTask{
			PID:    desktopPID,
			Name:   "Antigravity",
			Detail: detail,
			Bytes:  desktopBytes,
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
