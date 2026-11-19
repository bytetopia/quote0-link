package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bytetopia/quote0-link/app/internal/device"
	"github.com/bytetopia/quote0-link/app/internal/integration"
	"github.com/bytetopia/quote0-link/app/internal/maintenance"
	"github.com/bytetopia/quote0-link/app/internal/render"
	"github.com/bytetopia/quote0-link/app/internal/server"
)

const address = "127.0.0.1:45470"
const baseURL = "http://" + address

func configToken() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Quote0 Link", "api-token"), nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "emit" {
		if len(os.Args) >= 3 {
			emit(os.Args[2])
		}
		return
	}
	port := flag.String("port", "", "explicit serial device (for example COM4)")
	font := flag.String("font", defaultFont(), "optional TTF, OTF or TTC fallback font")
	headless := flag.Bool("headless", false, "start only the local server")
	flag.Parse()
	if err := run(*port, *font, *headless); err != nil {
		log.Fatal(err)
	}
}

func run(port, font string, headless bool) error {
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return fmt.Errorf("start local API: %w", err)
	}
	defer listener.Close()
	renderer, err := render.New(font)
	if err != nil {
		return err
	}
	tokenPath, err := configToken()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(tokenPath), 0700); err != nil {
		return err
	}
	var random [64]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(random[:32])
	adminToken := hex.EncodeToString(random[32:])
	if err := integration.AtomicWrite(tokenPath, []byte(token+"\n")); err != nil {
		return err
	}
	defer os.Remove(tokenPath)
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	link := &device.Link{Port: port}
	service := server.New(address, token, executable, renderer, link)
	testFrame, err := renderer.Draw(render.Card{Template: "test"})
	if err != nil {
		return err
	}
	manager := maintenance.New(link, executable, testFrame)
	service.EnableMaintenance(manager, adminToken)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go service.Run(ctx)
	httpServer := &http.Server{
		Handler: service.Handler(), ReadHeaderTimeout: 4 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 12 * time.Second, IdleTimeout: 30 * time.Second,
	}
	defer httpServer.Close()
	stopped := make(chan error, 1)
	go func() {
		stopped <- httpServer.Serve(listener)
		stop()
	}()
	log.Printf("Quote0 Link listening on %s; API token file: %s", baseURL, tokenPath)
	if headless {
		<-ctx.Done()
	} else {
		tray(ctx, manager.CloseIdle, func() {
			if err := openBrowser(baseURL + "/#" + token + "." + adminToken); err != nil {
				log.Print(err)
			}
		})
	}
	for !manager.CloseIdle() {
		time.Sleep(250 * time.Millisecond)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	default:
	}
	return nil
}

func emit(source string) {
	payload, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return
	}
	event, ok := integration.ReadEvent(source, payload)
	if !ok {
		return
	}
	path, err := configToken()
	if err != nil {
		return
	}
	token, err := os.ReadFile(path)
	if err != nil {
		return
	}
	body, _ := json.Marshal(event)
	request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/events", bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Transport:     &http.Transport{},
		Timeout:       450 * time.Millisecond,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	if response, err := client.Do(request); err == nil {
		_ = response.Body.Close()
	}
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	return command.Run()
}

func defaultFont() string {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/System/Library/Fonts/PingFang.ttc", "/System/Library/Fonts/Supplemental/Songti.ttc"}
	case "windows":
		candidates = []string{filepath.Join(os.Getenv("WINDIR"), "Fonts", "msyh.ttc")}
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
