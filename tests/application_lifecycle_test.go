package tests

import (
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestApplicationStartsAndShutsDown(t *testing.T) {
	databaseURL := os.Getenv("SERVICEOPS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("SERVICEOPS_TEST_DATABASE_URL is not set")
	}
	binary := filepath.Join(t.TempDir(), "serviceops-api")
	build := exec.Command("go", "build", "-o", binary, "./cmd/api")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build API: %v\n%s", err, output)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	command := exec.Command(binary)
	command.Env = append(os.Environ(),
		"HTTP_ADDR="+address,
		"DATABASE_URL="+databaseURL,
		"JWT_SECRET=0123456789abcdef0123456789abcdef",
	)
	command.Stdout, command.Stderr = io.Discard, io.Discard
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var response *http.Response
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		response, err = client.Get("http://" + address + "/api/v1/health/live")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		_ = command.Process.Kill()
		t.Fatalf("API did not become ready: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status=%d", response.StatusCode)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("API did not shut down")
	}
}
