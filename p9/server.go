// Package p9 implements the 9P filesystem for 9beads.
package p9

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"9beads/config"
	"9fans.net/go/plan9"
	"github.com/steveyegge/beads"
	"gopkg.in/yaml.v3"
)

const (
	qtDir  = plan9.QTDIR
	qtFile = plan9.QTFILE
)

type mount struct {
	name  string
	cwd   string
	store beads.Storage
}

type Server struct {
	mu       sync.RWMutex
	mounts   map[string]*mount
	beadsDir string
	events   *eventLog
}

type eventLog struct {
	mu   sync.Mutex
	buf  []byte
	vers uint32
}

func (el *eventLog) append(ev interface{}) {
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	data = append(data, '\n')
	el.mu.Lock()
	el.buf = append(el.buf, data...)
	el.vers++
	el.mu.Unlock()
}

func New() *Server {
	home, _ := os.UserHomeDir()
	return &Server{
		mounts:   make(map[string]*mount),
		beadsDir: filepath.Join(home, ".beads"),
		events:   &eventLog{},
	}
}

type fid struct {
	path     string
	qid      plan9.Qid
	mode     uint8
	writeBuf []byte
}

type connState struct {
	mu   sync.RWMutex
	fids map[uint32]*fid
}

func (s *Server) Serve(conn net.Conn) {
	defer conn.Close()
	cs := &connState{fids: make(map[uint32]*fid)}
	for {
		fc, err := plan9.ReadFcall(conn)
		if err != nil {
			if err != io.EOF {
				fmt.Fprintf(os.Stderr, "9beads: read: %v\n", err)
			}
			return
		}
		plan9.WriteFcall(conn, s.handle(cs, fc))
	}
}

func (s *Server) handle(cs *connState, fc *plan9.Fcall) *plan9.Fcall {
	switch fc.Type {
	case plan9.Tversion:
		msize := fc.Msize
		if msize > 65536 {
			msize = 65536
		}
		return &plan9.Fcall{Type: plan9.Rversion, Tag: fc.Tag, Msize: msize, Version: "9P2000"}
	case plan9.Tauth:
		return rerror(fc, "no auth required")
	case plan9.Tattach:
		return s.attach(cs, fc)
	case plan9.Twalk:
		return s.walk(cs, fc)
	case plan9.Topen:
		return s.open(cs, fc)
	case plan9.Tcreate:
		return rerror(fc, "create not supported")
	case plan9.Tread:
		return s.read(cs, fc)
	case plan9.Twrite:
		return s.write(cs, fc)
	case plan9.Tstat:
		return s.stat(cs, fc)
	case plan9.Twstat:
		return &plan9.Fcall{Type: plan9.Rwstat, Tag: fc.Tag}
	case plan9.Tflush:
		return &plan9.Fcall{Type: plan9.Rflush, Tag: fc.Tag}
	case plan9.Tclunk:
		return s.clunk(cs, fc)
	case plan9.Tremove:
		return rerror(fc, "remove not supported")
	default:
		return rerror(fc, "unsupported operation")
	}
}

func rerror(fc *plan9.Fcall, msg string) *plan9.Fcall {
	return &plan9.Fcall{Type: plan9.Rerror, Tag: fc.Tag, Ename: msg}
}

func qidPath(path string) uint64 {
	if path == "/" {
		return 0
	}
	h := fnv.New64a()
	h.Write([]byte(path))
	return h.Sum64()
}

func (s *Server) pathType(path string) string {
	if path == "/" {
		return "dir"
	}
	trimmed := strings.TrimPrefix(path, "/")
	parts := strings.SplitN(trimmed, "/", 3)
	switch {
	case len(parts) == 1:
		switch parts[0] {
		case "ctl", "mtab", "ready", "deferred", "closed", "events":
			return "file"
		default:
			s.mu.RLock()
			_, ok := s.mounts[parts[0]]
			s.mu.RUnlock()
			if ok {
				return "dir"
			}
		}
	case len(parts) == 2:
		s.mu.RLock()
		m, ok := s.mounts[parts[0]]
		s.mu.RUnlock()
		if !ok {
			return ""
		}
		switch parts[1] {
		case "ctl", "cwd", "list", "ready", "deferred", "closed":
			return "file"
		default:
			ctx := context.Background()
			issue, err := m.store.GetIssue(ctx, parts[1])
			if err == nil && issue != nil {
				return "file"
			}
		}
	}
	return ""
}

func pathParent(path string) string {
	if path == "/" {
		return "/"
	}
	i := strings.LastIndex(path, "/")
	if i == 0 {
		return "/"
	}
	return path[:i]
}

func pathJoin(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

func pathBase(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return path
	}
	return path[i+1:]
}

func (s *Server) attach(cs *connState, fc *plan9.Fcall) *plan9.Fcall {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	qid := plan9.Qid{Type: qtDir, Path: 0}
	cs.fids[fc.Fid] = &fid{path: "/", qid: qid}
	return &plan9.Fcall{Type: plan9.Rattach, Tag: fc.Tag, Qid: qid}
}

func (s *Server) walk(cs *connState, fc *plan9.Fcall) *plan9.Fcall {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	f, ok := cs.fids[fc.Fid]
	if !ok {
		return rerror(fc, "bad fid")
	}
	newf := &fid{path: f.path, qid: f.qid}
	if len(fc.Wname) == 0 {
		cs.fids[fc.Newfid] = newf
		return &plan9.Fcall{Type: plan9.Rwalk, Tag: fc.Tag, Wqid: []plan9.Qid{}}
	}
	wqids := make([]plan9.Qid, 0, len(fc.Wname))
	cur := f.path
	for _, name := range fc.Wname {
		var next string
		if name == ".." {
			next = pathParent(cur)
		} else {
			next = pathJoin(cur, name)
		}
		t := s.pathType(next)
		if t == "" {
			if len(wqids) == 0 {
				return rerror(fc, name+": file not found")
			}
			break
		}
		q := plan9.Qid{Path: qidPath(next)}
		if t == "dir" {
			q.Type = qtDir
		}
		wqids = append(wqids, q)
		cur = next
	}
	if len(wqids) == len(fc.Wname) {
		newf.path = cur
		newf.qid = wqids[len(wqids)-1]
		cs.fids[fc.Newfid] = newf
	}
	return &plan9.Fcall{Type: plan9.Rwalk, Tag: fc.Tag, Wqid: wqids}
}

func (s *Server) open(cs *connState, fc *plan9.Fcall) *plan9.Fcall {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	f, ok := cs.fids[fc.Fid]
	if !ok {
		return rerror(fc, "bad fid")
	}
	f.mode = fc.Mode
	if fc.Mode&plan9.OTRUNC != 0 {
		f.writeBuf = nil
	}
	return &plan9.Fcall{Type: plan9.Ropen, Tag: fc.Tag, Qid: f.qid}
}

func (s *Server) read(cs *connState, fc *plan9.Fcall) *plan9.Fcall {
	cs.mu.RLock()
	f, ok := cs.fids[fc.Fid]
	if !ok {
		cs.mu.RUnlock()
		return rerror(fc, "bad fid")
	}
	path := f.path
	isDir := f.qid.Type&qtDir != 0
	cs.mu.RUnlock()
	if isDir {
		data := s.readDir(path, fc.Offset, fc.Count)
		return &plan9.Fcall{Type: plan9.Rread, Tag: fc.Tag, Count: uint32(len(data)), Data: data}
	}
	content := s.readFile(path)
	var data []byte
	off := int(fc.Offset)
	if off < len(content) {
		end := off + int(fc.Count)
		if end > len(content) {
			end = len(content)
		}
		data = content[off:end]
	}
	return &plan9.Fcall{Type: plan9.Rread, Tag: fc.Tag, Count: uint32(len(data)), Data: data}
}

func (s *Server) write(cs *connState, fc *plan9.Fcall) *plan9.Fcall {
	cs.mu.Lock()
	f, ok := cs.fids[fc.Fid]
	if !ok {
		cs.mu.Unlock()
		return rerror(fc, "bad fid")
	}
	end := int(fc.Offset) + len(fc.Data)
	if end > len(f.writeBuf) {
		grown := make([]byte, end)
		copy(grown, f.writeBuf)
		f.writeBuf = grown
	}
	copy(f.writeBuf[fc.Offset:], fc.Data)
	cs.mu.Unlock()
	return &plan9.Fcall{Type: plan9.Rwrite, Tag: fc.Tag, Count: uint32(len(fc.Data))}
}

func (s *Server) stat(cs *connState, fc *plan9.Fcall) *plan9.Fcall {
	cs.mu.RLock()
	f, ok := cs.fids[fc.Fid]
	cs.mu.RUnlock()
	if !ok {
		return rerror(fc, "bad fid")
	}
	dir := s.makeStat(f.path)
	stat, err := dir.Bytes()
	if err != nil {
		return rerror(fc, err.Error())
	}
	return &plan9.Fcall{Type: plan9.Rstat, Tag: fc.Tag, Stat: stat}
}

func (s *Server) clunk(cs *connState, fc *plan9.Fcall) *plan9.Fcall {
	cs.mu.Lock()
	f, ok := cs.fids[fc.Fid]
	if ok {
		if len(f.writeBuf) > 0 {
			path := f.path
			data := make([]byte, len(f.writeBuf))
			copy(data, f.writeBuf)
			s.handleWrite(path, data)
		}
		delete(cs.fids, fc.Fid)
	}
	cs.mu.Unlock()
	return &plan9.Fcall{Type: plan9.Rclunk, Tag: fc.Tag}
}

func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.mounts {
		m.store.Close()
	}
}

func (s *Server) readFile(path string) []byte {
	trimmed := strings.TrimPrefix(path, "/")
	parts := strings.SplitN(trimmed, "/", 3)
	ctx := context.Background()
	if len(parts) == 1 {
		switch parts[0] {
		case "ctl":
			return nil
		case "mtab":
			return s.readMtab()
		case "ready":
			return s.readAggregateList(ctx, "ready")
		case "deferred":
			return s.readAggregateList(ctx, "deferred")
		case "closed":
			return s.readAggregateList(ctx, "closed")
		case "events":
			s.events.mu.Lock()
			buf := make([]byte, len(s.events.buf))
			copy(buf, s.events.buf)
			s.events.mu.Unlock()
			return buf
		}
		return nil
	}
	if len(parts) < 2 {
		return nil
	}
	s.mu.RLock()
	m, ok := s.mounts[parts[0]]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	if len(parts) == 2 {
		switch parts[1] {
		case "ctl":
			return nil
		case "cwd":
			return []byte(m.cwd + "\n")
		case "list":
			return s.readMountList(ctx, m, "list")
		case "ready":
			return s.readMountList(ctx, m, "ready")
		case "deferred":
			return s.readMountList(ctx, m, "deferred")
		case "closed":
			return s.readMountList(ctx, m, "closed")
		default:
			return s.readBead(ctx, m, parts[1])
		}
	}
	return nil
}

func (s *Server) readMtab() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b strings.Builder
	for _, m := range s.mounts {
		fmt.Fprintf(&b, "%s\t%s\n", m.name, m.cwd)
	}
	return []byte(b.String())
}

func (s *Server) readAggregateList(ctx context.Context, kind string) []byte {
	s.mu.RLock()
	mounts := make([]*mount, 0, len(s.mounts))
	for _, m := range s.mounts {
		mounts = append(mounts, m)
	}
	s.mu.RUnlock()
	var b strings.Builder
	for _, m := range mounts {
		b.Write(s.readMountList(ctx, m, kind))
	}
	return []byte(b.String())
}

func (s *Server) readMountList(ctx context.Context, m *mount, kind string) []byte {
	var issues []*beads.Issue
	var err error
	switch kind {
	case "list":
		issues, err = m.store.SearchIssues(ctx, "", beads.IssueFilter{
			ExcludeStatus: []beads.Status{beads.StatusClosed},
		})
	case "ready":
		issues, err = m.store.GetReadyWork(ctx, beads.WorkFilter{})
	case "deferred":
		issues, err = m.store.SearchIssues(ctx, "", beads.IssueFilter{
			Status: statusPtr(beads.StatusDeferred),
		})
	case "closed":
		issues, err = m.store.SearchIssues(ctx, "", beads.IssueFilter{
			Status: statusPtr(beads.StatusClosed),
		})
	}
	if err != nil {
		return nil
	}
	var b strings.Builder
	for _, iss := range issues {
		blockerCount := "-"
		deps, _ := m.store.GetDependencies(ctx, iss.ID)
		if len(deps) > 0 {
			blockerCount = fmt.Sprintf("%d", len(deps))
		}
		assignee := "-"
		if iss.Assignee != "" {
			assignee = iss.Assignee
		}
		updated := iss.UpdatedAt.Format("2006-01-02")
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\n",
			iss.ID, string(iss.Status), blockerCount, assignee, updated, iss.Title)
	}
	return []byte(b.String())
}

func statusPtr(s beads.Status) *beads.Status { return &s }

func (s *Server) readBead(ctx context.Context, m *mount, id string) []byte {
	issue, err := m.store.GetIssue(ctx, id)
	if err != nil || issue == nil {
		return nil
	}
	labels, _ := m.store.GetLabels(ctx, id)
	var blockerIDs []string
	depsWithMeta, _ := m.store.GetDependenciesWithMetadata(ctx, id)
	parentID := ""
	for _, d := range depsWithMeta {
		if d.DependencyType == beads.DepParentChild {
			parentID = d.ID
		} else if d.DependencyType == beads.DepBlocks {
			blockerIDs = append(blockerIDs, d.ID)
		}
	}
	fm := beadFrontmatter{
		ID:       issue.ID,
		Title:    issue.Title,
		Status:   string(issue.Status),
		Updated:  issue.UpdatedAt.Format("2006-01-02"),
		Parent:   parentID,
		Labels:   labels,
		Blockers: blockerIDs,
	}
	fmBytes, _ := yaml.Marshal(fm)
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n")
	if issue.Description != "" {
		b.WriteString(issue.Description)
		if !strings.HasSuffix(issue.Description, "\n") {
			b.WriteByte('\n')
		}
	}
	return []byte(b.String())
}

type beadFrontmatter struct {
	ID       string   `yaml:"id"`
	Title    string   `yaml:"title"`
	Status   string   `yaml:"status"`
	Updated  string   `yaml:"updated"`
	Parent   string   `yaml:"parent"`
	Labels   []string `yaml:"labels"`
	Blockers []string `yaml:"blockers"`
}

func (s *Server) readDir(path string, offset uint64, count uint32) []byte {
	var dirs []plan9.Dir
	mk := func(name, fpath string, isDir bool, mode plan9.Perm) plan9.Dir {
		q := plan9.Qid{Path: qidPath(fpath)}
		if isDir {
			q.Type = qtDir
		}
		return plan9.Dir{Qid: q, Mode: mode, Name: name, Uid: "beads", Gid: "beads", Muid: "beads"}
	}
	if path == "/" {
		dirs = append(dirs, mk("ctl", "/ctl", false, 0222))
		dirs = append(dirs, mk("mtab", "/mtab", false, 0444))
		dirs = append(dirs, mk("ready", "/ready", false, 0444))
		dirs = append(dirs, mk("deferred", "/deferred", false, 0444))
		dirs = append(dirs, mk("closed", "/closed", false, 0444))
		dirs = append(dirs, mk("events", "/events", false, 0444))
		s.mu.RLock()
		for name := range s.mounts {
			dirs = append(dirs, mk(name, "/"+name, true, plan9.DMDIR|0555))
		}
		s.mu.RUnlock()
	} else {
		trimmed := strings.TrimPrefix(path, "/")
		s.mu.RLock()
		m, ok := s.mounts[trimmed]
		s.mu.RUnlock()
		if ok {
			dirs = append(dirs, mk("ctl", path+"/ctl", false, 0222))
			dirs = append(dirs, mk("cwd", path+"/cwd", false, 0444))
			dirs = append(dirs, mk("list", path+"/list", false, 0444))
			dirs = append(dirs, mk("ready", path+"/ready", false, 0444))
			dirs = append(dirs, mk("deferred", path+"/deferred", false, 0444))
			dirs = append(dirs, mk("closed", path+"/closed", false, 0444))
			ctx := context.Background()
			issues, _ := m.store.SearchIssues(ctx, "", beads.IssueFilter{
				ExcludeStatus: []beads.Status{beads.StatusClosed},
			})
			for _, iss := range issues {
				dirs = append(dirs, mk(iss.ID, path+"/"+iss.ID, false, 0666))
			}
		}
	}
	var allData []byte
	for _, d := range dirs {
		b, err := d.Bytes()
		if err != nil {
			continue
		}
		allData = append(allData, b...)
	}
	if offset >= uint64(len(allData)) {
		return nil
	}
	remaining := allData[offset:]
	var result []byte
	for len(remaining) >= 2 {
		entrySize := int(remaining[0]) | int(remaining[1])<<8
		total := entrySize + 2
		if total > len(remaining) {
			break
		}
		if uint32(len(result)+total) > count {
			break
		}
		result = append(result, remaining[:total]...)
		remaining = remaining[total:]
	}
	return result
}

func (s *Server) makeStat(path string) plan9.Dir {
	base := pathBase(path)
	if path == "/" {
		base = "."
	}
	t := s.pathType(path)
	isDir := t == "dir"
	qid := plan9.Qid{Path: qidPath(path)}
	var mode plan9.Perm
	if isDir {
		qid.Type = qtDir
		mode = plan9.DMDIR | 0555
	} else {
		switch base {
		case "ctl":
			mode = 0222
		case "mtab", "cwd", "list", "ready", "deferred", "closed", "events":
			mode = 0444
		default:
			mode = 0666
		}
	}
	dir := plan9.Dir{Qid: qid, Mode: mode, Name: base, Uid: "beads", Gid: "beads", Muid: "beads"}
	if !isDir && mode != 0222 {
		content := s.readFile(path)
		dir.Length = uint64(len(content))
	}
	return dir
}

func (s *Server) handleWrite(path string, data []byte) {
	input := strings.TrimSpace(string(data))
	if input == "" {
		return
	}
	if path == "/ctl" {
		s.handleRootCtl(input)
		return
	}
	trimmed := strings.TrimPrefix(path, "/")
	parts := strings.SplitN(trimmed, "/", 3)
	if len(parts) < 2 {
		return
	}
	s.mu.RLock()
	m, ok := s.mounts[parts[0]]
	s.mu.RUnlock()
	if !ok {
		return
	}
	if parts[1] == "ctl" {
		s.handleMountCtl(m, input)
		return
	}
	s.handleBeadWrite(m, parts[1], data)
}

func (s *Server) handleRootCtl(input string) {
	args, err := ParseArgs(input)
	if err != nil || len(args) == 0 {
		return
	}
	switch args[0] {
	case "mount":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "9beads: mount requires <cwd>\n")
			return
		}
		cwd := args[1]
		name := ""
		if len(args) >= 3 {
			name = args[2]
		}
		s.doMount(cwd, name)
	case "umount":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "9beads: umount requires <name|cwd>\n")
			return
		}
		s.doUmount(args[1])
	}
}


func cwdToBeadsDir(beadsDir, cwd string) string {
	mangled := strings.ReplaceAll(cwd, "/", "-")
	return filepath.Join(beadsDir, mangled)
}

func shortID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func (s *Server) doMount(cwd, name string) {
	// Validate and resolve cwd against ProjectDirs
	if len(config.ProjectDirs) > 0 {
		resolved := false
		for _, dir := range config.ProjectDirs {
			if !strings.HasPrefix(cwd, dir+"/") {
				continue
			}
			rel := strings.TrimPrefix(cwd, dir+"/")
			parts := strings.SplitN(rel, "/", 3)
			switch len(parts) {
			case 1:
				if parts[0] != "" {
					resolved = true
				}
			case 2:
				cwd = filepath.Join(dir, parts[0])
				resolved = true
			}
			if resolved {
				break
			}
		}
		if !resolved {
			fmt.Fprintf(os.Stderr, "9beads: mount path must be at most 1 level below a project dir (%v)\n", config.ProjectDirs)
			return
		}
	}

	if name == "" {
		name = shortID()
	}

	s.mu.RLock()
	_, exists := s.mounts[name]
	s.mu.RUnlock()
	if exists {
		fmt.Fprintf(os.Stderr, "9beads: mount %q already exists\n", name)
		return
	}

	// Reject if cwd already mounted
	s.mu.RLock()
	for _, m := range s.mounts {
		if m.cwd == cwd {
			s.mu.RUnlock()
			fmt.Fprintf(os.Stderr, "9beads: path already mounted as %s\n", m.name)
			return
		}
	}
	s.mu.RUnlock()

	beadsPath := cwdToBeadsDir(s.beadsDir, cwd)
	ctx := context.Background()
	store, err := beads.OpenFromConfig(ctx, beadsPath)
	if err != nil {
		doltPath := filepath.Join(beadsPath, "dolt")
		store, err = beads.Open(ctx, doltPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "9beads: failed to open store for %s: %v\n", cwd, err)
			return
		}
	}
	m := &mount{name: name, cwd: cwd, store: store}
	s.mu.Lock()
	s.mounts[name] = m
	s.mu.Unlock()
	s.events.append(map[string]string{"type": "mount", "name": name, "cwd": cwd})
	fmt.Fprintf(os.Stderr, "9beads: mounted %s (%s)\n", name, cwd)
}

func (s *Server) doUmount(nameOrCwd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.mounts[nameOrCwd]; ok {
		m.store.Close()
		delete(s.mounts, nameOrCwd)
		s.events.append(map[string]string{"type": "umount", "name": nameOrCwd})
		return
	}
	for name, m := range s.mounts {
		if m.cwd == nameOrCwd {
			m.store.Close()
			delete(s.mounts, name)
			s.events.append(map[string]string{"type": "umount", "name": name})
			return
		}
	}
	fmt.Fprintf(os.Stderr, "9beads: umount: %q not found\n", nameOrCwd)
}

func (s *Server) handleMountCtl(m *mount, input string) {
	args, err := ParseArgs(input)
	if err != nil || len(args) == 0 {
		fmt.Fprintf(os.Stderr, "9beads: ctl parse error: %v\n", err)
		return
	}
	ctx := context.Background()
	cmd := args[0]
	args = args[1:]
	switch cmd {
	case "new":
		s.cmdNew(ctx, m, args)
	case "claim":
		s.cmdClaim(ctx, m, args)
	case "unclaim":
		s.cmdUnclaim(ctx, m, args)
	case "open":
		s.cmdOpen(ctx, m, args)
	case "defer":
		s.cmdDefer(ctx, m, args)
	case "reopen":
		s.cmdReopen(ctx, m, args)
	case "complete":
		s.cmdComplete(ctx, m, args)
	case "fail":
		s.cmdFail(ctx, m, args)
	case "update":
		s.cmdUpdate(ctx, m, args)
	case "delete":
		s.cmdDelete(ctx, m, args)
	case "comment":
		s.cmdComment(ctx, m, args)
	case "label":
		s.cmdLabel(ctx, m, args)
	case "unlabel":
		s.cmdUnlabel(ctx, m, args)
	case "set-capability":
		s.cmdSetCapability(ctx, m, args)
	case "dep":
		s.cmdDep(ctx, m, args)
	case "undep":
		s.cmdUndep(ctx, m, args)
	case "relate":
		s.cmdRelate(ctx, m, args)
	case "init":
		s.cmdInit(ctx, m, args)
	case "batch-create":
		s.cmdBatchCreate(ctx, m, args)
	default:
		fmt.Fprintf(os.Stderr, "9beads: unknown command %q\n", cmd)
	}
}

func (s *Server) generateID(ctx context.Context, m *mount, parent string) string {
	prefix, _ := m.store.GetConfig(ctx, "issue_prefix")
	if prefix == "" {
		prefix = "bd"
	}
	if parent != "" {
		deps, _ := m.store.GetDependents(ctx, parent)
		childNum := 1
		for _, d := range deps {
			if strings.HasPrefix(d.ID, parent+".") {
				childNum++
			}
		}
		return fmt.Sprintf("%s.%d", parent, childNum)
	}
	now := time.Now()
	content := fmt.Sprintf("%s|%d|%d", m.cwd, now.UnixNano(), len(prefix))
	hash := sha256.Sum256([]byte(content))
	num := new(big.Int).SetBytes(hash[:3])
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	base := big.NewInt(36)
	mod := new(big.Int)
	var chars []byte
	for num.Sign() > 0 {
		num.DivMod(num, base, mod)
		chars = append(chars, alphabet[mod.Int64()])
	}
	for i, j := 0, len(chars)-1; i < j; i, j = i+1, j-1 {
		chars[i], chars[j] = chars[j], chars[i]
	}
	id := string(chars)
	if len(id) < 4 {
		id = strings.Repeat("0", 4-len(id)) + id
	}
	if len(id) > 4 {
		id = id[len(id)-4:]
	}
	return prefix + "-" + id
}

func (s *Server) cmdNew(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "9beads: new requires title\n")
		return
	}
	title := args[0]
	desc := ""
	if len(args) >= 2 {
		desc = args[1]
	}
	pos, kv := ParseKV(args[2:])
	parentID := ""
	if len(pos) > 0 {
		parentID = pos[0]
	}
	id := s.generateID(ctx, m, parentID)
	now := time.Now()
	issue := &beads.Issue{
		ID: id, Title: title, Description: desc,
		Status: beads.StatusOpen, Priority: 2, IssueType: beads.TypeTask,
		CreatedAt: now, UpdatedAt: now,
	}
	if v, ok := kv["scope"]; ok {
		issue.SpecID = v
	}
	if err := m.store.CreateIssue(ctx, issue, "9beads"); err != nil {
		fmt.Fprintf(os.Stderr, "9beads: new: %v\n", err)
		return
	}
	if v, ok := kv["capability"]; ok {
		m.store.AddLabel(ctx, id, "capability:"+v, "9beads")
	}
	if parentID != "" {
		m.store.AddDependency(ctx, &beads.Dependency{
			IssueID: id, DependsOnID: parentID, Type: beads.DepParentChild,
			CreatedAt: now, CreatedBy: "9beads",
		}, "9beads")
	}
	if blockerStr, ok := kv["blockers"]; ok {
		for _, bid := range strings.Split(blockerStr, ",") {
			bid = strings.TrimSpace(bid)
			if bid != "" {
				m.store.AddDependency(ctx, &beads.Dependency{
					IssueID: id, DependsOnID: bid, Type: beads.DepBlocks,
					CreatedAt: now, CreatedBy: "9beads",
				}, "9beads")
			}
		}
	}
	s.events.append(map[string]string{"type": "created", "id": id, "mount": m.name})
}

func (s *Server) cmdClaim(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 { return }
	assignee := "9beads"
	if len(args) >= 2 { assignee = args[1] }
	m.store.UpdateIssue(ctx, args[0], map[string]interface{}{
		"status": string(beads.StatusInProgress), "assignee": assignee,
	}, "9beads")
	s.events.append(map[string]string{"type": "claimed", "id": args[0], "mount": m.name})
}

func (s *Server) cmdUnclaim(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 { return }
	m.store.UpdateIssue(ctx, args[0], map[string]interface{}{
		"status": string(beads.StatusOpen), "assignee": "",
	}, "9beads")
}

func (s *Server) cmdOpen(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 { return }
	m.store.UpdateIssue(ctx, args[0], map[string]interface{}{"status": string(beads.StatusOpen)}, "9beads")
}

func (s *Server) cmdDefer(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 { return }
	updates := map[string]interface{}{"status": string(beads.StatusDeferred)}
	if len(args) >= 3 && args[1] == "until" {
		if t, err := time.Parse(time.RFC3339, args[2]); err == nil {
			updates["defer_until"] = t
		}
	}
	m.store.UpdateIssue(ctx, args[0], updates, "9beads")
}

func (s *Server) cmdReopen(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 { return }
	m.store.UpdateIssue(ctx, args[0], map[string]interface{}{"status": string(beads.StatusOpen)}, "9beads")
}

func (s *Server) cmdComplete(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 { return }
	m.store.CloseIssue(ctx, args[0], "completed", "9beads", "")
	s.events.append(map[string]string{"type": "completed", "id": args[0], "mount": m.name})
}

func (s *Server) cmdFail(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 { return }
	reason := "failed"
	if len(args) >= 2 { reason = args[1] }
	m.store.CloseIssue(ctx, args[0], reason, "9beads", "")
	s.events.append(map[string]string{"type": "failed", "id": args[0], "mount": m.name})
}

func (s *Server) cmdUpdate(ctx context.Context, m *mount, args []string) {
	if len(args) < 3 {
		fmt.Fprintf(os.Stderr, "9beads: update requires <id> <field> <value>\n")
		return
	}
	if args[1] == "status" || args[1] == "assignee" {
		fmt.Fprintf(os.Stderr, "9beads: update cannot change %s\n", args[1])
		return
	}
	m.store.UpdateIssue(ctx, args[0], map[string]interface{}{args[1]: args[2]}, "9beads")
}

func (s *Server) cmdDelete(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 { return }
	m.store.DeleteIssue(ctx, args[0])
	s.events.append(map[string]string{"type": "deleted", "id": args[0], "mount": m.name})
}

func (s *Server) cmdComment(ctx context.Context, m *mount, args []string) {
	if len(args) < 2 { return }
	m.store.AddIssueComment(ctx, args[0], "9beads", args[1])
}

func (s *Server) cmdLabel(ctx context.Context, m *mount, args []string) {
	if len(args) < 2 { return }
	m.store.AddLabel(ctx, args[0], args[1], "9beads")
}

func (s *Server) cmdUnlabel(ctx context.Context, m *mount, args []string) {
	if len(args) < 2 { return }
	m.store.RemoveLabel(ctx, args[0], args[1], "9beads")
}

func (s *Server) cmdSetCapability(ctx context.Context, m *mount, args []string) {
	if len(args) < 2 { return }
	labels, _ := m.store.GetLabels(ctx, args[0])
	for _, l := range labels {
		if strings.HasPrefix(l, "capability:") {
			m.store.RemoveLabel(ctx, args[0], l, "9beads")
		}
	}
	m.store.AddLabel(ctx, args[0], "capability:"+args[1], "9beads")
}

func (s *Server) cmdDep(ctx context.Context, m *mount, args []string) {
	if len(args) < 2 { return }
	m.store.AddDependency(ctx, &beads.Dependency{
		IssueID: args[0], DependsOnID: args[1], Type: beads.DepBlocks,
		CreatedAt: time.Now(), CreatedBy: "9beads",
	}, "9beads")
}

func (s *Server) cmdUndep(ctx context.Context, m *mount, args []string) {
	if len(args) < 2 { return }
	m.store.RemoveDependency(ctx, args[0], args[1], "9beads")
}

func (s *Server) cmdRelate(ctx context.Context, m *mount, args []string) {
	if len(args) < 2 { return }
	m.store.AddDependency(ctx, &beads.Dependency{
		IssueID: args[0], DependsOnID: args[1], Type: beads.DepRelated,
		CreatedAt: time.Now(), CreatedBy: "9beads",
	}, "9beads")
}

func (s *Server) cmdInit(ctx context.Context, m *mount, args []string) {
	prefix := "bd"
	if len(args) >= 1 { prefix = args[0] }
	m.store.SetConfig(ctx, "issue_prefix", prefix)
}

func (s *Server) cmdBatchCreate(ctx context.Context, m *mount, args []string) {
	if len(args) < 1 { return }
	batch, err := ParseBatchCreate(strings.Join(args, " "))
	if err != nil {
		fmt.Fprintf(os.Stderr, "9beads: batch-create: %v\n", err)
		return
	}
	for _, item := range batch {
		title, _ := item["title"].(string)
		if title == "" { continue }
		desc, _ := item["description"].(string)
		newArgs := []string{title}
		if desc != "" { newArgs = append(newArgs, desc) }
		s.cmdNew(ctx, m, newArgs)
	}
}

func (s *Server) handleBeadWrite(m *mount, id string, data []byte) {
	content := string(data)
	if !strings.HasPrefix(content, "---\n") {
		m.store.UpdateIssue(context.Background(), id, map[string]interface{}{
			"description": strings.TrimSpace(content),
		}, "9beads")
		return
	}
	rest := content[4:]
	endIdx := strings.Index(rest, "\n---\n")
	if endIdx < 0 {
		if strings.HasSuffix(strings.TrimRight(rest, "\n"), "---") {
			endIdx = strings.LastIndex(rest, "---")
		}
		if endIdx < 0 {
			return
		}
	}
	fmStr := rest[:endIdx]
	body := ""
	if endIdx+5 <= len(rest) {
		body = rest[endIdx+5:]
	}
	var fm beadFrontmatter
	if err := yaml.Unmarshal([]byte(fmStr), &fm); err != nil {
		fmt.Fprintf(os.Stderr, "9beads: parse frontmatter for %s: %v\n", id, err)
		return
	}
	ctx := context.Background()
	updates := map[string]interface{}{
		"title":       fm.Title,
		"description": strings.TrimRight(body, "\n"),
	}
	if fm.Status != "" {
		updates["status"] = fm.Status
	}
	m.store.UpdateIssue(ctx, id, updates, "9beads")
	if fm.Labels != nil {
		existing, _ := m.store.GetLabels(ctx, id)
		existSet := make(map[string]bool)
		for _, l := range existing { existSet[l] = true }
		wantSet := make(map[string]bool)
		for _, l := range fm.Labels { wantSet[l] = true }
		for _, l := range fm.Labels {
			if !existSet[l] { m.store.AddLabel(ctx, id, l, "9beads") }
		}
		for _, l := range existing {
			if !wantSet[l] { m.store.RemoveLabel(ctx, id, l, "9beads") }
		}
	}
	if fm.Blockers != nil {
		existingDeps, _ := m.store.GetDependenciesWithMetadata(ctx, id)
		existBlockers := make(map[string]bool)
		for _, d := range existingDeps {
			if d.DependencyType == beads.DepBlocks { existBlockers[d.ID] = true }
		}
		wantBlockers := make(map[string]bool)
		for _, b := range fm.Blockers { wantBlockers[b] = true }
		for _, b := range fm.Blockers {
			if !existBlockers[b] {
				m.store.AddDependency(ctx, &beads.Dependency{
					IssueID: id, DependsOnID: b, Type: beads.DepBlocks,
					CreatedAt: time.Now(), CreatedBy: "9beads",
				}, "9beads")
			}
		}
		for bid := range existBlockers {
			if !wantBlockers[bid] { m.store.RemoveDependency(ctx, id, bid, "9beads") }
		}
	}
	s.events.append(map[string]string{"type": "updated", "id": id, "mount": m.name})
}
