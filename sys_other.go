//go:build !windows

package main

import (
	"fmt"
	"os/exec"
)

func hideCmd(cmd *exec.Cmd) *exec.Cmd { return cmd }

func msgBox(title, text string) { fmt.Println("\n  [" + title + "] " + text) }
