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

