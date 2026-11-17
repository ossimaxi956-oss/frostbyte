package main

// Internet-Link: startet einen kostenlosen Cloudflare-Tunnel (cloudflared),
// damit Freunde von ueberall ueber einen https-Link reinkommen.

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var (
	publicURL string // unter mu
	tunnelCmd *exec.Cmd
	tunnelRe  = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)
)

func cloudflaredPath() string {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	// liegt cloudflared schon neben der Frostbyte.exe? Dann das nehmen.
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return filepath.Join(dataDir, name)
}

func cloudflaredURL() string {
	base := "https://github.com/cloudflare/cloudflared/releases/latest/download/"
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		return base + "cloudflared-windows-amd64.exe"
	case "windows/386":
		return base + "cloudflared-windows-386.exe"
	case "windows/arm64":
		return base + "cloudflared-windows-amd64.exe"
	case "linux/arm64":
		return base + "cloudflared-linux-arm64"
	case "darwin/amd64", "darwin/arm64":
		return ""
	default:
		return base + "cloudflared-linux-amd64"
	}
}

func downloadCloudflared(dst string) error {
	url := cloudflaredURL()
	if url == "" {
		return fmt.Errorf("fuer dieses System gibt es keinen automatischen Download")
	}
	fmt.Println("  Internet-Link: lade Tunnel-Programm von Cloudflare (einmalig, ca. 60 MB) ...")
	cl := http.Client{Timeout: 10 * time.Minute}
	resp, err := cl.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Download-Fehler %d", resp.StatusCode)
	}
	tmp := dst + ".download"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil || n < 1000000 {
		os.Remove(tmp)
		if err == nil {
			err = fmt.Errorf("Download unvollstaendig")
		}
		return err
	}
	os.Chmod(tmp, 0755)
	return os.Rename(tmp, dst)
}

func setPublicURL(u string) {
	mu.Lock()
	publicURL = u
	sendAll("tunnel", M{"url": u})
	mu.Unlock()
}

func stopTunnel() {
	if tunnelCmd != nil && tunnelCmd.Process != nil {
		tunnelCmd.Process.Kill()
	}
}

// Raeumt einen alten, haengengebliebenen Tunnel von einem frueheren Start auf.
func killOldTunnel(pidFile string) {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		hideCmd(exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid))).Run()
	} else if p, err := os.FindProcess(pid); err == nil {
		p.Kill()
	}
	os.Remove(pidFile)
}

func startTunnel(port string) {
	if os.Getenv("FROSTBYTE_NO_TUNNEL") != "" {
		return
	}
	pidFile := filepath.Join(dataDir, "tunnel.pid")
	killOldTunnel(pidFile)
	bin := cloudflaredPath()
	if _, err := os.Stat(bin); err != nil {
		if err := downloadCloudflared(bin); err != nil {
			fmt.Println("  Internet-Link: geht gerade nicht (" + err.Error() + ").")
			fmt.Println("  Freunde im gleichen WLAN koennen trotzdem rein.")
			return
		}
	}
	wait := 5 * time.Second
	for {
		started := time.Now()
		cmd := hideCmd(exec.Command(bin, "tunnel", "--no-autoupdate", "--url", "http://localhost:"+port))
		stderr, _ := cmd.StderrPipe()
		cmd.Stdout = io.Discard
		if err := cmd.Start(); err != nil {
			fmt.Println("  Internet-Link: Tunnel startet nicht: " + err.Error())
			return
		}
		tunnelCmd = cmd
		os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
		sc := bufio.NewScanner(stderr)
		found := false
		for sc.Scan() {
			if !found {
				if u := tunnelRe.FindString(sc.Text()); u != "" {
					found = true
					setPublicURL(u)
					fmt.Println()
					fmt.Println("  *** INTERNET-LINK fuer Freunde (von ueberall): ***")
					fmt.Println("      " + u)
					fmt.Println("  (Der Link steht auch in der App. Er aendert sich bei jedem Start.)")
					fmt.Println()
				}
			}
		}
		cmd.Wait()
		os.Remove(pidFile)
		setPublicURL("")
		if time.Since(started) > 2*time.Minute {
			wait = 5 * time.Second
		} else if wait < 2*time.Minute {
			wait *= 2
		}
		fmt.Println("  Internet-Link wurde unterbrochen - neuer Versuch ...")
		time.Sleep(wait)
	}
}
