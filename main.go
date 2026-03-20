// 9beads - standalone 9P server for beads task filesystem
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"9fans.net/go/plan9"
	"9fans.net/go/plan9/client"
	ps "github.com/simonfxr/pubsub"
)

const serviceName = "beads"

// Qid paths for the filesystem
const (
	qidRoot = iota
	qidCtl
	qidMtab
	qidReady
	qidDeferred
	qidEvents
	qidMountBase = 0x1000 // mount directories start here
)

// Mount file indices
const (
	mountFileList = iota
	mountFileReady
	mountFileDeferred
	mountFileCwd
	mountFileCtl
	mountFileCount
)

var mountFileNames = []string{"list", "ready", "deferred", "cwd", "ctl"}

// Event types
const (
	EventBeadReady   = "BeadReady"
	EventBeadClaimed = "BeadClaimed"
)

type Event struct {
	ID     string `json:"id"`
	TS     int64  `json:"ts"`
	Source string `json:"source"`
	Type   string `json:"type"`
	Data   any    `json:"data"`
}

type Server struct {
	listener   net.Listener
	socketPath string
	beads      *BeadsFS
	events     *ps.Bus
	mu         sync.Mutex
}

func main() {
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: Beads <start|stop|fgstart|status>")
		os.Exit(1)
	}

	ns := client.Namespace()
	if ns == "" {
		log.Fatal("no namespace")
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
		fmt.Fprintln(os.Stderr, "usage: Beads <start|stop|fgstart|status>")
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
	cmd := exec.Command(exe, "fgstart")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Fatal(err)
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
	os.Remove(pidPath)
	os.Remove(sockPath)
	fmt.Println("9beads stopped")
}

func runServer(sockPath, pidPath string) {
	// Remove stale socket
	if _, err := os.Stat(sockPath); err == nil {
		os.Remove(sockPath)
	}

	// Write PID file
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Fatal(err)
	}

	srv := &Server{
		listener:   listener,
		socketPath: sockPath,
		beads:      NewBeadsFS(nil, context.Background()),
		events:     ps.NewBus(),
	}
	srv.beads.SetEventBus(srv.events)

	go srv.acceptLoop()

	log.Printf("9beads listening on %s", sockPath)

	// Setup FUSE mount
	mnt := os.Getenv("BEADS_9MOUNT")
	if mnt == "" {
		mnt = filepath.Join(os.Getenv("HOME"), "mnt", "beads")
	}
	var fuseCmd *exec.Cmd
	if err := os.MkdirAll(mnt, 0755); err != nil {
		log.Printf("warning: cannot create mount dir: %v", err)
	} else {
		fuseCmd = exec.Command("9pfuse", sockPath, mnt)
		if err := fuseCmd.Start(); err != nil {
			log.Printf("warning: 9pfuse failed: %v", err)
			fuseCmd = nil
		} else {
			log.Printf("mounted at %s", mnt)
		}
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("shutting down")
	if fuseCmd != nil {
		exec.Command("fusermount", "-u", mnt).Run()
		fuseCmd.Wait()
	}
	listener.Close()
	os.Remove(sockPath)
	os.Remove(pidPath)
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Printf("accept error: %v", err)
			}
			return
		}
		go s.handleConn(conn)
	}
}

type fidState struct {
	qid  plan9.Qid
	path string
	open bool
	// For events streaming
	eventCh     <-chan *Event
	eventCancel func()
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	fids := make(map[uint32]*fidState)
	var fidsMu sync.Mutex

	getFid := func(fid uint32) *fidState {
		fidsMu.Lock()
		defer fidsMu.Unlock()
		return fids[fid]
	}

	setFid := func(fid uint32, state *fidState) {
		fidsMu.Lock()
		defer fidsMu.Unlock()
		fids[fid] = state
	}

	delFid := func(fid uint32) {
		fidsMu.Lock()
		defer fidsMu.Unlock()
		if f := fids[fid]; f != nil && f.eventCancel != nil {
			f.eventCancel()
		}
		delete(fids, fid)
	}

	for {
		fcall, err := plan9.ReadFcall(conn)
		if err != nil {
			if err != io.EOF {
				log.Printf("read error: %v", err)
			}
			return
		}

		var resp plan9.Fcall
		resp.Tag = fcall.Tag

		switch fcall.Type {
		case plan9.Tversion:
			resp.Type = plan9.Rversion
			resp.Msize = fcall.Msize
			resp.Version = "9P2000"

		case plan9.Tattach:
			resp.Type = plan9.Rattach
			resp.Qid = plan9.Qid{Type: plan9.QTDIR, Path: qidRoot}
			setFid(fcall.Fid, &fidState{qid: resp.Qid, path: "/"})

		case plan9.Twalk:
			state := getFid(fcall.Fid)
			if state == nil {
				resp.Type = plan9.Rerror
				resp.Ename = "bad fid"
				break
			}

			newPath := state.path
			var qids []plan9.Qid

			for _, name := range fcall.Wname {
				qid, np, err := s.walk(newPath, name)
				if err != nil {
					break
				}
				qids = append(qids, qid)
				newPath = np
			}

			if len(qids) == 0 && len(fcall.Wname) > 0 {
				resp.Type = plan9.Rerror
				resp.Ename = "file not found"
				break
			}

			resp.Type = plan9.Rwalk
			resp.Wqid = qids
			if len(qids) == len(fcall.Wname) {
				newState := &fidState{path: newPath}
				if len(qids) > 0 {
					newState.qid = qids[len(qids)-1]
				} else {
					newState.qid = state.qid
				}
				setFid(fcall.Newfid, newState)
			}

		case plan9.Topen:
			state := getFid(fcall.Fid)
			if state == nil {
				resp.Type = plan9.Rerror
				resp.Ename = "bad fid"
				break
			}
			state.open = true
			resp.Type = plan9.Ropen
			resp.Qid = state.qid

		case plan9.Tread:
			state := getFid(fcall.Fid)
			if state == nil {
				resp.Type = plan9.Rerror
				resp.Ename = "bad fid"
				break
			}

			data, err := s.read(state, int64(fcall.Offset), fcall.Count)
			if err != nil {
				resp.Type = plan9.Rerror
				resp.Ename = err.Error()
				break
			}
			resp.Type = plan9.Rread
			resp.Data = data

		case plan9.Twrite:
			state := getFid(fcall.Fid)
			if state == nil {
				resp.Type = plan9.Rerror
				resp.Ename = "bad fid"
				break
			}

			n, err := s.write(state, fcall.Data)
			if err != nil {
				resp.Type = plan9.Rerror
				resp.Ename = err.Error()
				break
			}
			resp.Type = plan9.Rwrite
			resp.Count = uint32(n)

		case plan9.Tclunk:
			delFid(fcall.Fid)
			resp.Type = plan9.Rclunk

		case plan9.Tstat:
			state := getFid(fcall.Fid)
			if state == nil {
				resp.Type = plan9.Rerror
				resp.Ename = "bad fid"
				break
			}

			dir, err := s.stat(state.path)
			if err != nil {
				resp.Type = plan9.Rerror
				resp.Ename = err.Error()
				break
			}
			resp.Type = plan9.Rstat
			resp.Stat, _ = dir.Bytes()

		default:
			resp.Type = plan9.Rerror
			resp.Ename = "not implemented"
		}

		if err := plan9.WriteFcall(conn, &resp); err != nil {
			log.Printf("write error: %v", err)
			return
		}
	}
}

func (s *Server) walk(current, name string) (plan9.Qid, string, error) {
	if current == "/" {
		switch name {
		case "ctl":
			return plan9.Qid{Type: plan9.QTFILE, Path: qidCtl}, "/ctl", nil
		case "mtab":
			return plan9.Qid{Type: plan9.QTFILE, Path: qidMtab}, "/mtab", nil
		case "ready":
			return plan9.Qid{Type: plan9.QTFILE, Path: qidReady}, "/ready", nil
		case "deferred":
			return plan9.Qid{Type: plan9.QTFILE, Path: qidDeferred}, "/deferred", nil
		case "events":
			return plan9.Qid{Type: plan9.QTFILE, Path: qidEvents}, "/events", nil
		default:
			// Check if it's a mount name
			if s.beads.HasMount(name) {
				return plan9.Qid{Type: plan9.QTDIR, Path: qidMountBase}, "/" + name, nil
			}
		}
	} else if strings.HasPrefix(current, "/") && strings.Count(current, "/") == 1 {
		// Inside a mount directory
		mount := strings.TrimPrefix(current, "/")
		for i, fname := range mountFileNames {
			if name == fname {
				return plan9.Qid{Type: plan9.QTFILE, Path: qidMountBase + uint64(i) + 1}, current + "/" + name, nil
			}
		}
		// Check for bead ID
		if s.beads.HasBead(mount, name) {
			return plan9.Qid{Type: plan9.QTDIR, Path: qidMountBase + 0x100}, current + "/" + name, nil
		}
	} else if strings.Count(current, "/") == 2 {
		// Inside a bead directory
		parts := strings.Split(strings.TrimPrefix(current, "/"), "/")
		mount, beadID := parts[0], parts[1]
		if s.beads.HasBeadFile(mount, beadID, name) {
			return plan9.Qid{Type: plan9.QTFILE, Path: qidMountBase + 0x200}, current + "/" + name, nil
		}
	}

	return plan9.Qid{}, "", errors.New("not found")
}

func (s *Server) read(state *fidState, offset int64, count uint32) ([]byte, error) {
	path := state.path

	// Handle events streaming
	if path == "/events" {
		if state.eventCh == nil {
			ch := make(chan *Event, 64)
			sub := s.events.SubscribeChan("events", ch, ps.CloseOnUnsubscribe)
			state.eventCh = ch
			state.eventCancel = func() { s.events.Unsubscribe(sub) }
		}
		select {
		case e := <-state.eventCh:
			data, _ := json.Marshal(e)
			return append(data, '\n'), nil
		case <-time.After(30 * time.Second):
			return []byte{}, nil
		}
	}

	// Directory listings
	if state.qid.Type&plan9.QTDIR != 0 {
		return s.readDir(path, offset, count)
	}

	// File reads
	data, err := s.readFile(path)
	if err != nil {
		return nil, err
	}

	if offset >= int64(len(data)) {
		return []byte{}, nil
	}
	end := offset + int64(count)
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return data[offset:end], nil
}

func (s *Server) readDir(path string, offset int64, count uint32) ([]byte, error) {
	var entries []plan9.Dir

	if path == "/" {
		entries = []plan9.Dir{
			{Name: "ctl", Mode: 0644, Qid: plan9.Qid{Type: plan9.QTFILE, Path: qidCtl}},
			{Name: "mtab", Mode: 0444, Qid: plan9.Qid{Type: plan9.QTFILE, Path: qidMtab}},
			{Name: "ready", Mode: 0444, Qid: plan9.Qid{Type: plan9.QTFILE, Path: qidReady}},
			{Name: "deferred", Mode: 0444, Qid: plan9.Qid{Type: plan9.QTFILE, Path: qidDeferred}},
			{Name: "events", Mode: 0444, Qid: plan9.Qid{Type: plan9.QTFILE, Path: qidEvents}},
		}
		for name := range s.beads.ListMounts() {
			entries = append(entries, plan9.Dir{
				Name: name,
				Mode: plan9.DMDIR | 0755,
				Qid:  plan9.Qid{Type: plan9.QTDIR, Path: qidMountBase},
			})
		}
	} else if strings.Count(path, "/") == 1 {
		// Mount directory
		mount := strings.TrimPrefix(path, "/")
		for i, name := range mountFileNames {
			entries = append(entries, plan9.Dir{
				Name: name,
				Mode: 0644,
				Qid:  plan9.Qid{Type: plan9.QTFILE, Path: qidMountBase + uint64(i) + 1},
			})
		}
		// Add bead directories
		for _, id := range s.beads.ListBeadIDs(mount) {
			entries = append(entries, plan9.Dir{
				Name: id,
				Mode: plan9.DMDIR | 0755,
				Qid:  plan9.Qid{Type: plan9.QTDIR, Path: qidMountBase + 0x100},
			})
		}
	}

	var buf []byte
	for _, d := range entries {
		b, _ := d.Bytes()
		buf = append(buf, b...)
	}

	if offset >= int64(len(buf)) {
		return []byte{}, nil
	}
	end := offset + int64(count)
	if end > int64(len(buf)) {
		end = int64(len(buf))
	}
	return buf[offset:end], nil
}

func (s *Server) readFile(path string) ([]byte, error) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")

	switch {
	case path == "/ctl":
		return []byte("mount <path>\numount <name>\n"), nil
	case path == "/mtab":
		return s.beads.ReadMtab()
	case path == "/ready":
		return s.beads.ReadGlobalReady()
	case path == "/deferred":
		return s.beads.ReadGlobalDeferred()
	case len(parts) == 2:
		mount, file := parts[0], parts[1]
		return s.beads.ReadMountFile(mount, file)
	case len(parts) == 3:
		mount, beadID, file := parts[0], parts[1], parts[2]
		return s.beads.ReadBeadFile(mount, beadID, file)
	}

	return nil, errors.New("not found")
}

func (s *Server) write(state *fidState, data []byte) (int, error) {
	path := state.path
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")

	switch {
	case path == "/ctl":
		return s.beads.WriteCtl(string(data))
	case len(parts) == 2 && parts[1] == "ctl":
		return s.beads.WriteMountCtl(parts[0], string(data))
	case len(parts) == 3:
		mount, beadID, file := parts[0], parts[1], parts[2]
		return s.beads.WriteBeadFile(mount, beadID, file, data)
	}

	return 0, errors.New("permission denied")
}

func (s *Server) stat(path string) (plan9.Dir, error) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	name := parts[len(parts)-1]
	if name == "" {
		name = "/"
	}

	dir := plan9.Dir{Name: name, Uid: "beads", Gid: "beads", Muid: "beads"}

	switch {
	case path == "/":
		dir.Mode = plan9.DMDIR | 0755
		dir.Qid = plan9.Qid{Type: plan9.QTDIR, Path: qidRoot}
	case path == "/ctl":
		dir.Mode = 0644
		dir.Qid = plan9.Qid{Type: plan9.QTFILE, Path: qidCtl}
	case path == "/mtab", path == "/ready", path == "/deferred", path == "/events":
		dir.Mode = 0444
		dir.Qid = plan9.Qid{Type: plan9.QTFILE, Path: qidMtab}
	case len(parts) == 1 && s.beads.HasMount(parts[0]):
		dir.Mode = plan9.DMDIR | 0755
		dir.Qid = plan9.Qid{Type: plan9.QTDIR, Path: qidMountBase}
	default:
		return plan9.Dir{}, errors.New("not found")
	}

	return dir, nil
}
