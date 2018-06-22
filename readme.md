
## watch-file

Run commands after a file changes.

#### Usage

```
 watch-file [FLAGS] <files or directories...>

 FLAGS:
    -c --cmd        commands to run
    -p --pattern    pattern to filter the changed files
```

#### Samples


```
# if you are modify a main.c file, and want to run it after every edit. you can use this tool like this.
watch-file -c='gcc -o main main.c' -c='./main' main.c
```

#### Command Hooks

*{check}* check last command exec result. stop if an error occur.

*{kill}* kill the running command, it used when you run a long wait program. like web etc.
```
$ # like a go web demo. has 2 steps, build and run.
$ # use {check} to stop if the build is error. and use {kill} stop the running web.
$ watch-file -p='**.go' -c='go build -o demo .' -c='{check}' -c='{kill}' -c='./demo'

$ # another is a flask demo with out debug mode
$ watch-file -p='**.py' -c='{kill}' -c='python app.py'
```

#### Placeholders

*{file}* the changed file path

*{name}* the changed file name without ext.

*{ext}* the modified file ext.

*{dir}* the modified file dir path.

```
$ # use like 'scss --watch' command
$ watch-file -p='**.scss' -c='sass {file} {fileDir}/{fileDir}.css'
```

#### Files Pattern

Please refer to [glob](https://github.com/gobwas/glob)


#### Install
```
go get github.com/heramerom/watch-file
```