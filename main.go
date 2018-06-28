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

type Commands []string

func (c *Commands) String() string {
	return ""
}

func (c *Commands) Set(value string) error {
	*c = append(*c, value)
	return nil
}

func beginWatcher(watcher *fsnotify.Watcher, match glob.Glob, except glob.Glob) {
	var preCancelFunc *context.CancelFunc
	var lock sync.Mutex
	var running bool
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
			lock.Lock()
			if running {
				lock.Unlock()
				break
			}
			running = true
			lock.Unlock()
			ctx, cancel := context.WithCancel(context.Background())
			go func(fn *context.CancelFunc) {
				<-time.After(time.Second * time.Duration(delay))
				running = false
				defer cancel()
				HandleChangedFile(event, fn, ctx)
			}(preCancelFunc)
			preCancelFunc = &cancel

		case err := <-watcher.Errors:
			fmt.Println("watch error:", err)
		}
	}
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

func HandleChangedFile(event fsnotify.Event, preCancelFunc *context.CancelFunc, cancelCtx context.Context) {
	defer func() {
		if x := recover(); x != nil {
			fmt.Println("panic:", x)
		}
	}()
	var err error
	for _, cmd := range commands {
		cmd = replacePlaceholder(cmd, event.Name)
		debug("command:", cmd)
		if cmd == "{check}" {
			if err != nil {
				break
			}
			continue
		}
		if cmd == "{kill}" {
			if preCancelFunc != nil {
				(*preCancelFunc)()
			}
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer cancel()
			defer close(done)
			commands := strings.Split(cmd, " ")
			cmd := exec.CommandContext(ctx, commands[0], commands[1:]...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stdout
			err = cmd.Run()
			if err != nil {
				fmt.Println("run command error:", err.Error())
			}
		}()

		select {
		case <-cancelCtx.Done():
			cancel()
			return
		case <-done:
			continue
		}
	}
}

func replacePlaceholder(cmd, name string) string {
	cmd = strings.Replace(cmd, "{ext}", path.Ext(name), -1)
	cmd = strings.Replace(cmd, "{name}", strings.Replace(path.Base(name), path.Ext(name), "", -1), -1)
	cmd = strings.Replace(cmd, "{dir}", path.Dir(name), -1)
	cmd = strings.Replace(cmd, "{file}", name, -1)
	return cmd
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

func main() {

	flag.Var(&commands, "cmd", "commands to run")
	flag.Var(&commands, "c", "commands to run")
	flag.StringVar(&pattern, "pattern", "*", "pattern to filter changed file")
	flag.StringVar(&pattern, "p", "*", "pattern to filter changed file")
	flag.StringVar(&except, "except", "", "except files")
	flag.StringVar(&except, "e", "", "except files")
	flag.BoolVar(&verbose, "v", false, "verbose")
	flag.IntVar(&delay, "delay", 1, "delay to exec commands")

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
		fmt.Println("can not complie pattern:", err.Error())
		os.Exit(1)
	}

	var exceptMatch glob.Glob
	if except != "" {
		exceptMatch, err = glob.Compile(except)
		if err != nil {
			fmt.Println("can not complie except")
			os.Exit(1)
		}
	}

	go beginWatcher(watcher, match, exceptMatch)

	addWatchFilesOrDirectories(args, watcher)

	waitForSignalNotify()
	debug("goodbye")
}
