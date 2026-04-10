// 9beads - 9P server for beads task tracking
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"9fans.net/go/plan9/client"
	p9 "9beads/p9"
)

const serviceName = "beads"

var mountPath = flag.String("mount", "", "FUSE mount path (default: $BEADS_9MOUNT or ~/mnt/beads)")

func main() {
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: 9beads <start|fgstart|stop|status>")
		os.Exit(1)
	}

	ns := client.Namespace()
	if ns == "" {
		fmt.Fprintln(os.Stderr, "no namespace")
		os.Exit(1)
	}

	sockPath := filepath.Join(ns, serviceName)
	pidPath := filepath.Join(ns, serviceName+".pid")

	switch flag.Arg(0) {
	case "start":
		if isRunning(sockPath) {
			fmt.Println("9beads already running")
			os.Exit(0)
		}
		daemonize(pidPath)
	case "fgstart":
		if isRunning(sockPath) {
			fmt.Println("9beads already running")
			os.Exit(0)
		}
		runServer(sockPath, pidPath)
	case "stop":
		stopServer(sockPath, pidPath)
	case "status":
		if isRunning(sockPath) {
			fmt.Println("9beads running")
		} else {
			fmt.Println("9beads not running")
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "usage: 9beads <start|fgstart|stop|status>")
		os.Exit(1)
	}
}

func isRunning(sockPath string) bool {
	conn, err := net.Dial("unix", sockPath)
	if err == nil {
		conn.Close()
		return true
	}
	return false
}

func daemonize(pidPath string) {
	exe, _ := os.Executable()
	args := []string{"fgstart"}
	if *mountPath != "" {
		args = append(args, "-mount", *mountPath)
	}
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("9beads started (pid %d)\n", cmd.Process.Pid)
}

func stopServer(sockPath, pidPath string) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("9beads not running")
		return
	}
	var pid int
	fmt.Sscanf(string(data), "%d", &pid)
	if pid > 0 {
		syscall.Kill(pid, syscall.SIGTERM)
	}
	os.Remove(sockPath)
	os.Remove(pidPath)
	fmt.Println("9beads stopped")
}

func runServer(sockPath, pidPath string) {
	if _, err := os.Stat(sockPath); err == nil {
		os.Remove(sockPath)
	}

	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	srv := p9.New()

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		os.Exit(1)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go srv.Serve(conn)
		}
	}()

	fmt.Printf("9beads listening on %s\n", sockPath)

	// FUSE mount
	mnt := *mountPath
	if mnt == "" {
		mnt = os.Getenv("BEADS_9MOUNT")
	}
	if mnt == "" {
		home, _ := os.UserHomeDir()
		mnt = filepath.Join(home, "mnt", "beads")
	}
	var fuseCmd *exec.Cmd
	if err := os.MkdirAll(mnt, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "warning: cannot create mount dir: %v\n", err)
	} else {
		fuseCmd = exec.Command("9pfuse", sockPath, mnt)
		if err := fuseCmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: 9pfuse: %v\n", err)
			fuseCmd = nil
		} else {
			fmt.Printf("mounted at %s\n", mnt)
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	fmt.Println("shutting down")
	srv.Close()
	if fuseCmd != nil {
		exec.Command("fusermount", "-u", mnt).Run()
		fuseCmd.Wait()
	}
	listener.Close()
	os.Remove(sockPath)
	os.Remove(pidPath)
}
