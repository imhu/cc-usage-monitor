//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modKernel32ConPTY               = windows.NewLazySystemDLL("kernel32.dll")
	procUpdateProcThreadAttributePT = modKernel32ConPTY.NewProc("UpdateProcThreadAttribute")
)

type ConPTYProcess struct {
	hPC      windows.Handle
	hProcess windows.Handle
	hThread  windows.Handle
	hJob     windows.Handle
	inWrite  windows.Handle
	outRead  windows.Handle
	pid      uint32

	done      chan struct{}
	closeOnce sync.Once
}

// StartConPTYCommand starts an executable inside a hidden Windows ConPTY (Pseudo Console)
// bound to a kill-on-close Job Object so child trees are cleaned up reliably.
func StartConPTYCommand(ctx context.Context, exe string, args []string, workDir string) (*ConPTYProcess, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	launchExe := exe
	launchArgs := append([]string{exe}, args...)
	ext := strings.ToLower(filepath.Ext(exe))
	if ext == ".cmd" || ext == ".bat" {
		sysDir, err := windows.GetSystemDirectory()
		if err != nil {
			sysDir = `C:\Windows\System32`
		}
		launchExe = filepath.Join(sysDir, "cmd.exe")
		cmdInner := windows.ComposeCommandLine(append([]string{exe}, args...))
		launchArgs = []string{launchExe, "/C", cmdInner}
	}

	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("create input pipe: %w", err)
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		return nil, fmt.Errorf("create output pipe: %w", err)
	}

	var hPC windows.Handle
	size := windows.Coord{X: 120, Y: 30}
	if err := windows.CreatePseudoConsole(size, inRead, outWrite, 0, &hPC); err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		_ = windows.CloseHandle(outWrite)
		return nil, fmt.Errorf("CreatePseudoConsole: %w", err)
	}
	// Close the console-end handles after CreatePseudoConsole duplicates them.
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hPC)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("NewProcThreadAttributeList: %w", err)
	}
	defer attrList.Delete()

	// PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE expects lpValue to be the HPCON handle itself.
	r1, _, callErr := procUpdateProcThreadAttributePT.Call(
		uintptr(unsafe.Pointer(attrList.List())),
		0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(hPC),
		unsafe.Sizeof(hPC),
		0,
		0,
	)
	if r1 == 0 {
		windows.ClosePseudoConsole(hPC)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("UpdateProcThreadAttribute: %w", callErr)
	}

	var exePtr *uint16
	if launchExe != "" {
		exePtr, err = windows.UTF16PtrFromString(launchExe)
		if err != nil {
			windows.ClosePseudoConsole(hPC)
			_ = windows.CloseHandle(inWrite)
			_ = windows.CloseHandle(outRead)
			return nil, err
		}
	}
	cmdLinePtr, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(launchArgs))
	if err != nil {
		windows.ClosePseudoConsole(hPC)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}

	var dirPtr *uint16
	if workDir != "" {
		dirPtr, _ = windows.UTF16PtrFromString(workDir)
	}

	envBlock, err := buildConPTYEnvBlock()
	if err != nil {
		windows.ClosePseudoConsole(hPC)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}
	var envPtr *uint16
	if len(envBlock) > 0 {
		envPtr = &envBlock[0]
	}

	si := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:         uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:      windows.STARTF_USESHOWWINDOW,
			ShowWindow: windows.SW_HIDE,
		},
		ProcThreadAttributeList: attrList.List(),
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)

	if err := windows.CreateProcess(exePtr, cmdLinePtr, nil, nil, false, flags, envPtr, dirPtr, &si.StartupInfo, &pi); err != nil {
		windows.ClosePseudoConsole(hPC)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, fmt.Errorf("CreateProcess: %w", err)
	}

	hJob, jobErr := windows.CreateJobObject(nil, nil)
	if jobErr == nil {
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, setErr := windows.SetInformationJobObject(
			hJob,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); setErr == nil {
			_ = windows.AssignProcessToJobObject(hJob, pi.Process)
		}
	}

	_, _ = windows.ResumeThread(pi.Thread)

	p := &ConPTYProcess{
		hPC:      hPC,
		hProcess: pi.Process,
		hThread:  pi.Thread,
		hJob:     hJob,
		inWrite:  inWrite,
		outRead:  outRead,
		pid:      pi.ProcessId,
		done:     make(chan struct{}),
	}

	// Drain ConPTY output asynchronously so child never blocks on full terminal buffer.
	go func(h windows.Handle) {
		buf := make([]byte, 4096)
		for {
			var n uint32
			if err := windows.ReadFile(h, buf, &n, nil); err != nil || n == 0 {
				return
			}
		}
	}(outRead)

	go func() {
		defer close(p.done)
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
	}()

	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				p.Close()
			case <-p.done:
			}
		}()
	}

	return p, nil
}

func (p *ConPTYProcess) PID() uint32 {
	if p == nil {
		return 0
	}
	return p.pid
}

func (p *ConPTYProcess) IsRunning() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *ConPTYProcess) Done() <-chan struct{} {
	return p.done
}

func (p *ConPTYProcess) Close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		if p.hJob != 0 {
			_ = windows.CloseHandle(p.hJob)
			p.hJob = 0
		}
		if p.hProcess != 0 {
			_ = windows.TerminateProcess(p.hProcess, 1)
		}
		if p.hPC != 0 {
			windows.ClosePseudoConsole(p.hPC)
			p.hPC = 0
		}
		if p.inWrite != 0 {
			_ = windows.CloseHandle(p.inWrite)
			p.inWrite = 0
		}
		if p.outRead != 0 {
			_ = windows.CloseHandle(p.outRead)
			p.outRead = 0
		}
		if p.hThread != 0 {
			_ = windows.CloseHandle(p.hThread)
			p.hThread = 0
		}
		if p.hProcess != 0 {
			_ = windows.CloseHandle(p.hProcess)
			p.hProcess = 0
		}
	})
}

func buildConPTYEnvBlock() ([]uint16, error) {
	rawEnv := os.Environ()
	env := make([]string, 0, len(rawEnv)+2)
	hasTerm := false
	for _, kv := range rawEnv {
		key := kv
		if eq := strings.IndexByte(kv, '='); eq >= 0 {
			key = kv[:eq]
		}
		upperKey := strings.ToUpper(key)
		if strings.HasPrefix(upperKey, "ANTIGRAVITY_") || strings.HasPrefix(upperKey, "AGY_") {
			continue
		}
		if upperKey == "TERM" {
			hasTerm = true
		}
		env = append(env, kv)
	}
	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	// Prevent agy CLI from spawning a detached background auto-updater console process (--bg-updater) every ~15m.
	// Note: agy.exe compares AGY_CLI_DISABLE_AUTO_UPDATE strictly against "true".
	env = append(env, "AGY_CLI_DISABLE_AUTO_UPDATE=true")
	var out []uint16
	for _, kv := range env {
		u, err := windows.UTF16FromString(kv)
		if err != nil {
			return nil, err
		}
		out = append(out, u...)
	}
	out = append(out, 0)
	return out, nil
}

// VisibleConsoleProcess represents a process launched inside its own visible Windows console window
// bound to a kill-on-close Job Object so closing it tears down the full child tree.
type VisibleConsoleProcess struct {
	hProcess windows.Handle
	hThread  windows.Handle
	hJob     windows.Handle
	pid      uint32

	done      chan struct{}
	closeOnce sync.Once
}

// StartVisibleConsoleCommand opens a new visible Windows console running exe/script.
func StartVisibleConsoleCommand(ctx context.Context, exe string, args []string, workDir string) (*VisibleConsoleProcess, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	launchExe := exe
	launchArgs := append([]string{exe}, args...)
	ext := strings.ToLower(filepath.Ext(exe))
	if ext == ".cmd" || ext == ".bat" {
		sysDir, err := windows.GetSystemDirectory()
		if err != nil {
			sysDir = `C:\Windows\System32`
		}
		launchExe = filepath.Join(sysDir, "cmd.exe")
		cmdInner := windows.ComposeCommandLine(append([]string{exe}, args...))
		launchArgs = []string{launchExe, "/C", cmdInner}
	}

	var exePtr *uint16
	var err error
	if launchExe != "" {
		exePtr, err = windows.UTF16PtrFromString(launchExe)
		if err != nil {
			return nil, err
		}
	}
	cmdLinePtr, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(launchArgs))
	if err != nil {
		return nil, err
	}
	var dirPtr *uint16
	if workDir != "" {
		dirPtr, _ = windows.UTF16PtrFromString(workDir)
	}
	envBlock, err := buildConPTYEnvBlock()
	if err != nil {
		return nil, err
	}
	var envPtr *uint16
	if len(envBlock) > 0 {
		envPtr = &envBlock[0]
	}

	si := windows.StartupInfo{
		Cb:         uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Flags:      windows.STARTF_USESHOWWINDOW,
		ShowWindow: windows.SW_SHOWNORMAL,
	}
	var pi windows.ProcessInformation
	flags := uint32(windows.CREATE_NEW_CONSOLE | windows.CREATE_UNICODE_ENVIRONMENT | windows.CREATE_SUSPENDED)
	if err := windows.CreateProcess(exePtr, cmdLinePtr, nil, nil, false, flags, envPtr, dirPtr, &si, &pi); err != nil {
		return nil, fmt.Errorf("CreateProcess: %w", err)
	}

	hJob, jobErr := windows.CreateJobObject(nil, nil)
	if jobErr == nil {
		var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, setErr := windows.SetInformationJobObject(
			hJob,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); setErr == nil {
			_ = windows.AssignProcessToJobObject(hJob, pi.Process)
		}
	}

	_, _ = windows.ResumeThread(pi.Thread)

	p := &VisibleConsoleProcess{
		hProcess: pi.Process,
		hThread:  pi.Thread,
		hJob:     hJob,
		pid:      pi.ProcessId,
		done:     make(chan struct{}),
	}

	go func() {
		defer close(p.done)
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
	}()

	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				p.Close()
			case <-p.done:
			}
		}()
	}

	return p, nil
}

func (p *VisibleConsoleProcess) Done() <-chan struct{} {
	return p.done
}

func (p *VisibleConsoleProcess) Close() {
	if p == nil {
		return
	}
	p.closeOnce.Do(func() {
		if p.hJob != 0 {
			_ = windows.CloseHandle(p.hJob)
			p.hJob = 0
		}
		if p.hProcess != 0 {
			_ = windows.TerminateProcess(p.hProcess, 1)
		}
		if p.hThread != 0 {
			_ = windows.CloseHandle(p.hThread)
			p.hThread = 0
		}
		if p.hProcess != 0 {
			_ = windows.CloseHandle(p.hProcess)
			p.hProcess = 0
		}
	})
}
