
## watch-file

This is tool is used to exec some commands after edit a file.

Eg. if you are modify a *.c* file, and want to run it after every edit. you can use this tool like this.
```
watch-file -f='main.c' -c='gcc -o main main.c' -c='./main'
```

#### Command Hooks

*{stop if error}* stop if exec command error

*{stop pre cmd}* stop pre-command, it used when you run a long wait program. like web etc.
```
watch-file -p='**.go' -c='go build -o demo .' -c='{stop if error}' -c='{stop pre cmd}' -c='./demo' # demo is a web program
```

#### Placeholders

*{file}* the modified file

*{fileName}* the modified file name without ext.

*{fileExt}* the modified file ext.

*{fileDir}* the modified file dir path.

*{dir}* the watched dir path.

```
watch-file -p='**.scss' -c='sass {file} {fileDir}/{fileDir}.css'  # just a demo, also you can use 'sass --watch'
```

#### Files Pattern

Please refer to [glob](https://github.com/gobwas/glob)


#### Install
```
go get github.com/heramerom/watch-file
```