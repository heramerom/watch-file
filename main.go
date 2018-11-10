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

func (c *Command) build(name string) []string {
	cmd := string(*c)
	cmd = strings.Replace(cmd, "{ext}", path.Ext(name), -1)
	cmd = strings.Replace(cmd, "{name}", strings.Replace(path.Base(name), path.Ext(name), "", -1), -1)
	cmd = strings.Replace(cmd, "{dir}", path.Dir(name), -1)
	cmd = strings.Replace(cmd, "{file}", name, -1)
	return strings.Split(cmd, " ")
}

func (c *Command) exec(filename string, ctx context.Context) error {
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

func beginWatcher(watcher *fsnotify.Watcher, match glob.Glob, except glob.Glob, fileChan chan string) {
	for {
		select {
		case event := <-watcher.Events:
			if match != nil {
				if !match.Match(event.Name) {
					continue
				}
			}
			if except != nil {
				if except.Match(event.Name) {
					continue
				}
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
		if canExec(running, delay) {
			continue
		}
		go func(fn *context.CancelFunc) {
			preCancelFunc = HandleChangedFile(filename, fn)
		}(preCancelFunc)
	}
}

func canExec(running runningFlag, delay int) bool {
	if running.isRunning() {
		return false
	}
	running.setRunning(true)
	<-time.After(time.Second * time.Duration(delay))
	running.setRunning(false)
	return true
}

func addWatchFilesOrDirectories(paths []string, watcher *fsnotify.Watcher) {
	for _, p := range paths {
		isDir, err := isDirectory(p)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		if isDir {
			addWatchDirectory(p, watcher)
		} else {
			addWatchFile(p, watcher)
		}
	}
}

func isDirectory(path string) (is bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	is = info.IsDir()
	return
}

func addWatchDirectory(dir string, watcher *fsnotify.Watcher) {
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if isHiddenFile(path) {
			return nil
		}
		if info.IsDir() {
			debug("watch directory:", path)
			err = watcher.Add(path)
			if err != nil {
				fmt.Println(err.Error())
				os.Exit(1)
			}
		}
		return nil
	})
}

func addWatchFile(f string, watcher *fsnotify.Watcher) {
	if err := watcher.Add(f); err != nil {
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

func waitForSignalNotify() {
	ch := make(chan os.Signal)
	defer close(ch)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGQUIT, syscall.SIGKILL)
	<-ch
}

func HandleChangedFile(filename string, preCancelFunc *context.CancelFunc) (killFunc *context.CancelFunc) {
	defer func() {
		if x := recover(); x != nil {
			fmt.Println("panic:", x)
		}
	}()
	var err error
	for _, cmd := range commands {
		if cmd.isCheck() {
			if err != nil {
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

		ctx, cancel := context.WithCancel(context.Background())
		killFunc = &cancel
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

func main() {

	flag.Var(&commands, "cmd", "commands to run")
	flag.Var(&commands, "c", "commands to run")
	flag.StringVar(&pattern, "pattern", "*", "pattern to filter changed file")
	flag.StringVar(&pattern, "p", "*", "pattern to filter changed file")
	flag.StringVar(&except, "except", "", "except files")
	flag.StringVar(&except, "e", "", "except files")
	flag.BoolVar(&verbose, "v", false, "verbose")
	flag.IntVar(&delay, "delay", 1, "delay to exec commands")
	flag.BoolVar(&execOnInit, "init", true, "exec commands when init")

	flag.Parse()

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

	fileChan := make(chan string, 1)
	defer close(fileChan)
	go beginWatcher(watcher, match, exceptMatch, fileChan)
	go watchFileChanged(fileChan)

	if execOnInit {
		fileChan <- ""
	}

	addWatchFilesOrDirectories(args, watcher)

	waitForSignalNotify()
	debug("goodbye")
}
