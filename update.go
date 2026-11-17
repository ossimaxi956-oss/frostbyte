package main

// Auto-Update: schaut bei GitHub nach einer neuen Version,
// laedt sie herunter, ersetzt die .exe und startet neu.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const UpdateRepo = "ossimaxi956-oss/frostbyte"

var updating bool

type ghRelease struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func versionNum(tag string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(strings.TrimPrefix(tag, "v"), "V"))
	return n
}

// Raeumt die alte .exe vom letzten Update weg.
func cleanupOldExe() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	old := exe + ".old"
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(old); os.IsNotExist(err) {
			return
		}
		if os.Remove(old) == nil {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func checkUpdate() (*ghRelease, string) {
	cl := http.Client{Timeout: 20 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+UpdateRepo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Frostbyte/"+Version)
	resp, err := cl.Do(req)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, ""
	}
	var r ghRelease
	if json.NewDecoder(resp.Body).Decode(&r) != nil {
		return nil, ""
	}
	if versionNum(r.Tag) <= versionNum(Version) {
		return nil, ""
	}
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, "Frostbyte.exe") {
			return &r, a.URL
		}
	}
	return nil, ""
}

func applyUpdate(rel *ghRelease, url string, srv *http.Server) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	fmt.Println("  Update gefunden: " + rel.Tag + " - lade herunter ...")
	cl := http.Client{Timeout: 10 * time.Minute}
	resp, err := cl.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	tmp := exe + ".new"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil || n < 1000000 {
		os.Remove(tmp)
		return
	}
	// Alle Nutzer informieren
	mu.Lock()
	updating = true
	sendAll("updating", M{"version": rel.Tag})
	mu.Unlock()
	time.Sleep(1500 * time.Millisecond)

	// Laufende .exe zur Seite schieben (geht unter Windows auch waehrend sie laeuft)
	os.Remove(exe + ".old")
	if err := os.Rename(exe, exe+".old"); err != nil {
		os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(exe+".old", exe)
		return
	}
	fmt.Println("  Update installiert - starte neu ...")
	srv.Close()
	stopTunnel()
	writeDB()
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	cmd.Env = append(os.Environ(), "FROSTBYTE_UPDATED=1")
	if cmd.Start() != nil {
		os.Rename(exe+".old", exe)
		return
	}
	os.Exit(0)
}

// Prueft beim Start und dann alle 30 Minuten.
func updateLoop(srv *http.Server) {
	if runtime.GOOS != "windows" || os.Getenv("FROSTBYTE_NO_UPDATE") != "" {
		return
	}
	go cleanupOldExe()
	time.Sleep(10 * time.Second)
	for {
		if rel, url := checkUpdate(); rel != nil {
			if activeCalls() > 0 { // nicht mitten im Anruf updaten
				time.Sleep(3 * time.Minute)
				continue
			}
			applyUpdate(rel, url, srv)
		}
		time.Sleep(30 * time.Minute)
	}
}

func activeCalls() int {
	vmu.Lock()
	defer vmu.Unlock()
	n := 0
	for _, r := range rooms {
		if len(r.conns) > 0 {
			n++
		}
	}
	return n
}
