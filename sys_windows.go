//go:build windows

package main

import (
	"os/exec"
	"syscall"
	"unsafe"
)

// Programme (cloudflared, taskkill, netstat) ohne schwarzes Fenster starten
func hideCmd(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return cmd
}

// Windows-Meldungsfenster (statt Konsole)
func msgBox(title, text string) {
	u := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(text)
	u.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x40)
}
