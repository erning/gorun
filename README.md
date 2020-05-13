# gorun

## What is it?
gorun is a tool enabling one to put a "bang line" in the source code of a Go program to run it, or to run such a source code file explicitly. It was created in an attempt to make experimenting with Go more appealing to people used to Python and similar languages which operate most visibly with source code.

## Example
As an example, copy the following content to a file named "hello.go" (or "hello", if you prefer):

```go
#!/usr/bin/env gorun

package main

func main() {
    println("Hello world!")
}
```

Then, simply run it:

```
$ chmod +x hello.go
$ ./hello.go
Hello world!
```

## Features
gorun will:

  * write files under a safe directory in $TMPDIR (or /tmp), so that the actual script location isn't touched (may be read-only)
  * avoid races between parallel executions of the same file
  * automatically clean up old compiled files that remain unused for some time (without races)
  * replace the process rather than using a child
  * pass arguments to the compiled application properly
  * handle well GOROOT, GOROOT_FINAL and the location of the toolchain
  * support embedded go.mod, go.sum and environment variables used for compiling - can ensure a repeatable build
  * support more than one source file

## Is it slow?
No, it's not, thanks to the Go (gc) compiler suite, which compiles code surprisingly fast.

Here is a trivial/non-scientific comparison with Python:

```
$ time ./gorun hello.go
Hello world!
./gorun hello.go  0.03s user 0.00s system 74% cpu 0.040 total

$ time ./gorun hello.go
Hello world!
./gorun hello.go  0.00s user 0.00s system 0% cpu 0.003 total

$ time python -c 'print "Hello world!"'
Hello world!
python -c 'print "Hello world!"'  0.01s user 0.00s system 63% cpu 0.016 total

$ time python -c 'print "Hello world!"'
Hello world!
python -c 'print "Hello world!"'  0.00s user 0.01s system 64% cpu 0.016 total
```

Note how the second run is significantly faster than the first one. This happens because a cached version of the file is used after the first compilation.

gorun will correctly recompile the file whenever necessary.

## Where are the compiled files kept?
They are kept under $TMPDIR (or tmp), in a directory named after the hostname and user id executing the file.

You can remove these files, but there's no reason to do this. These compiled files will be garbage collected by gorun itself after a while once they stop being used. This is done in a fast and safe way so that concurrently executing scripts will not fail to execute.

## Ubuntu packages
There are Ubuntu packages available that include gorun:

```
$ sudo add-apt-repository ppa:gophers/go
$ sudo apt-get update
$ sudo apt-get install golang
```

## How to build and install gorun from source
Just use "go get" as usual:

**Option 1:** from Launchpad (requires [Bazaar](http://wiki.bazaar.canonical.com/)):

```
$ go get launchpad.net/gorun
```

**Option 2:** from Github (requires [Git](http://git-scm.com)):

```
$ go get github.com/erning/gorun
```

## Reporting bugs
Please report bugs at: https://launchpad.net/gorun

## License

gorun is licensed under the GPL.

This document is licensed under Creative Commons Attribution-ShareAlike 3.0 License.

## Contact
To get in touch, send a message to gustavo.niemeyer@canonical.com

## Repeatable builds
To protect against changing/different dependencies compiled with the script, it supports embedding
go.mod, go.sum contents and environment variables in the file as a comment. Fictitious example:

    // go.mod >>>
    // module github.com/a/b
    // go 1.13
    // require github.com/c/d v0.0.0-20200225084820-12345affa
    // require mycompany.com/e/f v0.0.0-20200225084120-1849135
    // <<< go.mod
    //
    // go.env >>>
    // GOPRIVATE=mycompany.com
    // GO111MODULE=on
    // <<< go.env
    //
    // go.sum >>>
    // github.com/c v0.0.0-20190308221718-c2843e01d9a2/go.mod h1:djNgcEr1/C05ACkg1iLfiJU5Ep61QUkGW8qpdssI0+w=
    // <<< go.sum

    package main

    import (
    ...

To support this, an environment variable ```GORUN_ARGS``` can be set to diff, extract or embed these sections:

Check whether the files go.mod/go.sum on disc match the embedded sections inside the source file

    GORUN_ARGS="-diff -noRun" gorun sourcefile.go
    gorun -diff sourcefile.go

Put any go.mod/go.sum files in the same directory as the sourcefile.go file in the comments in the file

    GORUN_ARGS="-embed -noRun" gorun sourcefile.go
    gorun -embed -noRun sourcefile.go

Extract the commented sections of go.mod/go.sum to files alongside the source file:

    GORUN_ARGS="-extract -noRun" gorun sourcefile.go
    gorun -extract -noRun sourcefile.go

A default that allows developing and auto including the go.mod/go.sum in to the source file would be:

    GORUN_ARGS="-embed" gorun sourcefile.go

For safety, the above by default will not embed files if the sourcefile lives under /bin /sbin /opt or /usr.
Set -embedIgnoreRegex to '.*' to override this behaviour

## Extra source files

gorun supports including any extra source files when the "script" grows a little too large for a single file,
and also allows multiple "scripts" to all live at the same place on the PATH, e.g. all in /usr/local/bin.

Place any extra source files in a directory with the same name as the source .go, replacing ".go" with "_". e.g.

    ./httpServe.go
    ./httpServe_/net/auth.go
    ./httpServe_/net/reply.go
    ./httpServe_/db/sql.go

Then import "httpServe/httpServe_/net" in httpServe.go etc.

