package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/gobwas/glob"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type runningFlag struct {
	running bool
	sync.Mutex
}

func (r *runningFlag) isRunning() (running bool) {
	r.Lock()
	running = r.running
	r.Unlock()
	return
}

func (r *runningFlag) setRunning(running bool) {
	r.Lock()
	r.running = running
	r.Unlock()
	return
}

type Command string

func (c *Command) isCheck() bool {
	return (*c) == "{check}"
}

func (c *Command) isKill() bool {
	return (*c) == "{kill}"
}

func (c *Command) isWait() bool {
	return (*c) == "{wait}"
}

func (c *Command) build(name string) []string {
	cmd := string(*c)
	cmd = strings.Replace(cmd, "{ext}", path.Ext(name), -1)
	cmd = strings.Replace(cmd, "{name}", strings.Replace(path.Base(name), path.Ext(name), "", -1), -1)
	cmd = strings.Replace(cmd, "{dir}", path.Dir(name), -1)
	cmd = strings.Replace(cmd, "{file}", name, -1)
	return strings.Split(cmd, " ")
}

func (c *Command) hasHook() bool {
	for _, hook := range []string{"{ext}", "{name}", "{dir}", "{file}"} {
		if strings.Contains(string(*c), hook) {
			return true
		}
	}
	return false
}

func (c *Command) exec(filename string, ctx context.Context) error {
	if filename == "" && c.hasHook() {
		return nil
	}
	commands := c.build(filename)
	cmd := exec.CommandContext(ctx, commands[0], commands[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout
	return cmd.Run()
}

type Commands []Command

func (c *Commands) String() string {
	return ""
}

func (c *Commands) Set(value string) error {
	*c = append(*c, Command(value))
	return nil
}

type FileRepo struct {
	Paths  map[string]struct{}
	Files  map[string]struct{}
	Match  glob.Glob
	Except glob.Glob
}

func NewFileRepo(match, except glob.Glob) *FileRepo {
	return &FileRepo{
		Paths:  make(map[string]struct{}),
		Files:  make(map[string]struct{}),
		Match:  match,
		Except: except,
	}
}

func (fr *FileRepo) IsMatch(pth string) bool {
	_, ok := fr.Files[pth]
	if ok {
		return true
	}
	_, ok = fr.Paths[filepath.Dir(pth)]
	if ok {
		if fr.Match != nil && !fr.Match.Match(pth) {
			return false
		}
		if fr.Except != nil && fr.Except.Match(pth) {
			return false
		}
		return true
	}
	return false
}

func beginWatcher(watcher *fsnotify.Watcher, repo *FileRepo, fileChan chan string) {
	for {
		select {
		case event := <-watcher.Events:
			if !repo.IsMatch(event.Name) {
				continue
			}
			if event.Op == fsnotify.Chmod {
				continue
			}
			fileChan <- event.Name
		case err := <-watcher.Errors:
			fmt.Println("watch error:", err)
		}
	}
}

func watchFileChanged(fileChan chan string) {
	var preCancelFunc *context.CancelFunc
	var running runningFlag
	for {
		filename := <-fileChan
		if running.isRunning() {
			continue
		}
		running.setRunning(true)
		cancelCtx, cancel := context.WithCancel(context.Background())
		go func(fn *context.CancelFunc) {
			<-time.After(time.Second * time.Duration(delay))
			running.setRunning(false)
			defer cancel()
			pf := HandleChangedFile(filename, fn, cancelCtx)
			if pf != nil {
				preCancelFunc = pf
			}
		}(preCancelFunc)
		preCancelFunc = &cancel
	}
}

func addWatchFilesOrDirectories(paths []string, repo *FileRepo, watcher *fsnotify.Watcher) {
	for _, pth := range paths {
		isDir, err := isDirectory(pth)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		if isDir {
			pth = filepath.Dir(filepath.Join(pth, "tmp"))
			repo.Paths[pth] = struct{}{}
			addWatchDirectory(pth, repo, watcher)
		} else {
			repo.Files[pth] = struct{}{}
			addWatchFile(pth, watcher)
		}
	}
}

func isDirectory(path string) (is bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	info.Name()
	is = info.IsDir()
	return
}

func addWatchDirectory(dir string, repo *FileRepo, watcher *fsnotify.Watcher) {
	filepath.Walk(dir, func(pth string, info os.FileInfo, err error) error {
		if isHiddenFile(pth) {
			return nil
		}
		if info.IsDir() {
			debug("watch directory:", pth)
			repo.Paths[pth] = struct{}{}
			err = watcher.Add(pth)
			if err != nil {
				fmt.Println(err.Error())
				os.Exit(1)
			}
		}
		return nil
	})
}

func addWatchFile(pth string, watcher *fsnotify.Watcher) {
	if err := watcher.Add(filepath.Dir(pth)); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func isHiddenFile(path string) bool {
	ps := strings.Split(path, string(filepath.Separator))
	for _, p := range ps {
		if len(p) > 1 && p[0] == '.' {
			return true
		}
	}
	return false
}

func waitingForSignalNotify() {
	ch := make(chan os.Signal)
	defer close(ch)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGKILL)
	<-ch
}

func HandleChangedFile(filename string, preCancelFunc *context.CancelFunc,
	cancelCtx context.Context) (cancelFunc *context.CancelFunc) {
	defer func() {
		if x := recover(); x != nil {
			fmt.Println("panic:", x)
		}
	}()
	var err error
	for _, cmd := range commands {
		if cmd.isCheck() {
			if err != nil {
				cancelFunc = preCancelFunc
				break
			}
			continue
		}
		if cmd.isKill() {
			if preCancelFunc != nil {
				(*preCancelFunc)()
			}
			continue
		}

		if cmd.isWait() {
			time.Sleep(time.Second * time.Duration(waitSeconds))
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer cancel()
			defer close(done)
			err = cmd.exec(filename, ctx)
			if err != nil {
				fmt.Println("run command error:", err.Error())
			}
		}()
		select {
		case <-cancelCtx.Done():
			cancel()
			break
		case <-done:
			continue
		}
	}
	return
}

func debug(msg ...interface{}) {
	if verbose {
		fmt.Println(msg...)
	}
}

var commands Commands
var pattern string
var verbose bool
var delay int
var except string
var execOnInit bool
var waitSeconds int

func init() {
	flag.Var(&commands, "cmd", "commands to run")
	flag.Var(&commands, "c", "commands to run")
	flag.StringVar(&pattern, "pattern", "*", "pattern to filter changed file")
	flag.StringVar(&pattern, "p", "*", "pattern to filter changed file")
	flag.StringVar(&except, "except", "", "except files")
	flag.StringVar(&except, "e", "", "except files")
	flag.BoolVar(&verbose, "v", false, "verbose")
	flag.IntVar(&delay, "delay", 1, "delay to exec commands")
	flag.BoolVar(&execOnInit, "init", true, "exec commands when init")
	flag.IntVar(&waitSeconds, "wait", 1, "wait some seconds after kill pre-command")

	flag.Parse()
}

func main() {

	args := flag.Args()
	if len(args) == 0 {
		args = []string{"./"}
	}

	debug("match pattern:", pattern)
	debug("commands:")
	for _, cmd := range commands {
		debug("\t", cmd)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Println("can not init file watcher:", err.Error())
		os.Exit(1)
	}
	defer watcher.Close()

	match, err := glob.Compile(pattern)
	if err != nil {
		fmt.Println("can not compile pattern:", err.Error())
		os.Exit(1)
	}

	var exceptMatch glob.Glob
	if except != "" {
		exceptMatch, err = glob.Compile(except)
		if err != nil {
			fmt.Println("can not compile except")
			os.Exit(1)
		}
	}

	repo := NewFileRepo(match, exceptMatch)

	fileChan := make(chan string, 16)
	defer close(fileChan)
	go beginWatcher(watcher, repo, fileChan)
	go watchFileChanged(fileChan)

	if execOnInit {
		fileChan <- ""
	}

	addWatchFilesOrDirectories(args, repo, watcher)

	waitingForSignalNotify()
	debug("goodbye")
}
