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
	"syscall"
)

type Commands []string

func (c *Commands) String() string {
	return ""
}

func (c *Commands) Set(value string) error {
	*c = append(*c, value)
	return nil
}

var commands Commands
var pattern string
var watchDirectory string
var verbose bool

func main() {

	flag.Var(&commands, "cmd", "commands to run")
	flag.Var(&commands, "c", "commands to run")
	flag.StringVar(&pattern, "pattern", "*", "pattern to match file")
	flag.StringVar(&pattern, "p", "*", "pattern to match file")
	flag.StringVar(&watchDirectory, "dir", "./", "directory to watch")
	flag.StringVar(&watchDirectory, "d", "./", "directory to watch")
	flag.BoolVar(&verbose, "v", false, "verbose")

	flag.Parse()

	debug("match pattern:", pattern)
	debug("watch dir:", watchDirectory)
	commands = replaceDirectoryPlaceholder(commands, watchDirectory)
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

	go beginWatcher(watcher, match)

	addWatchDirectory(watchDirectory, watcher)

	waitForSignalNotify()
	debug("goodbye")
}

func beginWatcher(watcher *fsnotify.Watcher, match glob.Glob) {
	var preCancelFunc *context.CancelFunc
	for {
		select {
		case event := <-watcher.Events:
			if event.Op != fsnotify.Create && event.Op != fsnotify.Write && event.Op != fsnotify.Remove {
				continue
			}
			if !match.Match(event.Name) {
				continue
			}
			debug("match file:", event.Name)

			ctx, cancel := context.WithCancel(context.Background())
			go func(fn *context.CancelFunc) {
				defer cancel()
				HandleChangedFile(event, fn, ctx)
			}(preCancelFunc)
			preCancelFunc = &cancel
		case err := <-watcher.Errors:
			println("error:", err)
		}
	}
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
				println(err)
				os.Exit(1)
			}
		}
		return nil
	})
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
		debug("run command:", cmd)
		if cmd == "{stop if error}" {
			if err != nil {
				break
			}
			continue
		}
		if cmd == "{stop pre cmd}" {
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

func replaceDirectoryPlaceholder(commands Commands, dir string) Commands {
	r := make([]string, 0, len(commands))
	for _, cmd := range commands {
		r = append(r, strings.Replace(cmd, "{dir}", dir, -1))
	}
	return Commands(r)
}

func replacePlaceholder(cmd, name string) string {
	cmd = strings.Replace(cmd, "{fileExt}", path.Ext(name), -1)
	cmd = strings.Replace(cmd, "{fileName}", strings.Replace(path.Base(name), path.Ext(name), "", -1), -1)
	cmd = strings.Replace(cmd, "{fileDir}", path.Dir(name), -1)
	cmd = strings.Replace(cmd, "{file}", name, -1)
	return cmd
}

func debug(msg ...interface{}) {
	if verbose {
		fmt.Println(msg...)
	}
}
