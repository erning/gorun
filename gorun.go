//
// gorun - Script-like runner for Go source files.
//
//   https://wiki.ubuntu.com/gorun
//
// Copyright (c) 2011 Canonical Ltd.
//
// Written by Gustavo Niemeyer <gustavo.niemeyer@canonical.com>
//
package main

// This program is free software: you can redistribute it and/or modify it 
// under the terms of the GNU General Public License version 3, as published 
// by the Free Software Foundation.
// 
// This program is distributed in the hope that it will be useful, but 
// WITHOUT ANY WARRANTY; without even the implied warranties of 
// MERCHANTABILITY, SATISFACTORY QUALITY, or FITNESS FOR A PARTICULAR 
// PURPOSE.  See the GNU General Public License for more details.
// 
// You should have received a copy of the GNU General Public License along 
// with this program.  If not, see <http://www.gnu.org/licenses/>.

import (
	"exec"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gorun <source file> [...]")
		os.Exit(1)
	}

	err := Run(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.String())
		os.Exit(1)
	}

	panic("unreachable")
}

// Run compiles and links the Go source file on args[0] and
// runs it with arguments args[1:].
func Run(args []string) os.Error {
	sourcefile := args[0]
	rundir, runfile, err := RunFile(sourcefile)
	if err != nil {
		return err
	}

	compile := false

	// Nanoseconds must be called before Stat of sourcefile below,
	// so that changing the file between Stat and Chtimes still
	// causes the file to be updated on the next run.
	now := time.Nanoseconds()

	sstat, err := os.Stat(sourcefile)
	if err != nil {
		return err
	}

	rstat, err := os.Stat(runfile)
	switch {
	case err != nil:
		compile = true
	case !rstat.IsRegular():
		return os.NewError("not a file: " + runfile)
	case rstat.Mtime_ns < sstat.Mtime_ns || rstat.Permission()&0700 != 0700:
		compile = true
	default:
		// We have spare cycles. Maybe remove old files.
		if err := os.Chtimes(runfile, now, now); err == nil {
			CleanDir(rundir, now)
		}
	}

	for retry := 3; retry > 0; retry-- {
		if compile {
			err := Compile(sourcefile, runfile)
			if err != nil {
				return err
			}
			// If sourcefile was changed, will be updated on next run.
			os.Chtimes(runfile, sstat.Mtime_ns, sstat.Mtime_ns)
		}

		err = os.Exec(runfile, args, os.Environ())
		if perr, ok := err.(*os.PathError); ok && perr.Error == os.ENOENT {
			// Got cleaned up under our feet.
			compile = true
			continue
		}
		break
	}
	if err != nil {
		panic("exec returned but succeeded")
	}
	return err
}

// Compile compiles and links sourcefile and atomically renames the
// resulting binary to runfile.
func Compile(sourcefile, runfile string) (err os.Error) {
	pid := strconv.Itoa(os.Getpid())

	content, err := ioutil.ReadFile(sourcefile)
	if len(content) > 2 && content[0] == '#' && content[1] == '!' {
		content[0] = '/'
		content[1] = '/'
		sourcefile = runfile + "." + pid + ".go"
		ioutil.WriteFile(sourcefile, content, 0600)
		defer os.Remove(sourcefile)
	}

	bindir := filepath.Join(runtime.GOROOT(), "bin")
	n := TheChar()
	gc := filepath.Join(bindir, n+"g")
	ld := filepath.Join(bindir, n+"l")
	if _, err := os.Stat(gc); err != nil {
		if gc, err = exec.LookPath(n + "g"); err != nil {
			return os.NewError("can't find " + n + "g")
		}
	}
	if _, err := os.Stat(ld); err != nil {
		if ld, err = exec.LookPath(n + "l"); err != nil {
			return os.NewError("can't find " + n + "l")
		}
	}
	gcout := runfile + "." + pid + "." + n
	ldout := runfile + "." + pid
	err = Exec([]string{gc, "-o", gcout, sourcefile})
	if err != nil {
		return err
	}
	defer os.Remove(gcout)
	err = Exec([]string{ld, "-o", ldout, gcout})
	if err != nil {
		return err
	}
	return os.Rename(ldout, runfile)
}

// Exec runs args[0] with args[1:] arguments and passes through
// stdout and stderr.
func Exec(args []string) os.Error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	base := filepath.Base(args[0])
	if w, ok := err.(*os.Waitmsg); ok && w.ExitStatus() != 0 {
		return os.NewError(base + " exited with status " + strconv.Itoa(w.ExitStatus()))
	}
	if err != nil {
		return os.NewError("failed to run " + base + ": " + err.String())
	}
	return nil
}

// RunFile returns the directory and file location where the binary generated
// for sourcefile should be put.  In case the directory does not yet exist, it
// will be created by RunDir.
func RunFile(sourcefile string) (rundir, runfile string, err os.Error) {
	rundir, err = RunDir()
	if err != nil {
		return "", "", err
	}
	sourcefile, err = filepath.Abs(sourcefile)
	if err != nil {
		return "", "", err
	}
	sourcefile, err = filepath.EvalSymlinks(sourcefile)
	if err != nil {
		return "", "", err
	}
	runfile = strings.Replace(sourcefile, "%", "%%", -1)
	runfile = strings.Replace(runfile, string(filepath.Separator), "%", -1)
	runfile = filepath.Join(rundir, runfile)
	return rundir, runfile, nil
}

func canWrite(stat *os.FileInfo, euid, egid int) bool {
	perm := stat.Permission()
	return perm&02 != 0 || perm&020 != 0 && egid == stat.Gid || perm&0200 != 0 && euid == stat.Uid
}

// RunDir returns the directory where binary files generates should be put.
// In case a safe directory isn't found, one will be created.
func RunDir() (rundir string, err os.Error) {
	tempdir := os.TempDir()
	euid := os.Geteuid()
	stat, err := os.Stat(tempdir)
	if err != nil || !stat.IsDirectory() || !canWrite(stat, euid, os.Getegid()) {
		return "", os.NewError("can't write on directory: " + tempdir)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "", os.NewError("can't get hostname: " + err.String())
	}
	prefix := "gorun-" + hostname + "-" + strconv.Itoa(euid)
	suffix := runtime.GOOS + "_" + runtime.GOARCH
	prefixi := prefix
	var i uint
	for {
		rundir = filepath.Join(tempdir, prefixi, suffix)

		// A directory is only considered safe if the owner matches the
		// user running the script and its permissions prevent someone
		// else from writing on it.
		stat, err := os.Stat(rundir)
		if err == nil && stat.IsDirectory() && stat.Permission() == 0700 && stat.Uid == euid {
			return rundir, nil
		}
		if perr, ok := err.(*os.PathError); ok && perr.Error == os.ENOENT {
			err := os.MkdirAll(rundir, 0700)
			if err == nil {
				return rundir, nil
			}
		}
		i++
		prefixi = prefix + "-" + strconv.Uitoa(i)
	}
	panic("unreachable")
}

const CleanFileDelay = 1e9 * 60 * 60 * 24 * 7 // 1 week

// CleanDir removes binary files under rundir in case they were not
// accessed for more than CleanFileDelay nanoseconds.  A last-cleaned
// marker file is created so that the next verification is only done
// after CleanFileDelay nanoseconds.
func CleanDir(rundir string, now int64) os.Error {
	cleanedfile := filepath.Join(rundir, "last-cleaned")
	if info, err := os.Stat(cleanedfile); err == nil && info.Mtime_ns > now-CleanFileDelay {
		// It's been cleaned recently.
		return nil
	}
	f, err := os.Create(cleanedfile)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte(strconv.Itoa64(now)))
	f.Close()
	if err != nil {
		return err
	}

	// Look for expired files.
	d, err := os.Open(rundir)
	if err != nil {
		return err
	}
	infos, err := d.Readdir(-1)
	expired := now - CleanFileDelay
	for _, info := range infos {
		if info.Atime_ns < expired {
			os.Remove(filepath.Join(rundir, info.Name))
		}
	}
	return nil
}

// TheChar returns the magic architecture char.
func TheChar() string {
	switch runtime.GOARCH {
	case "386":
		return "8"
	case "amd64":
		return "6"
	case "arm":
		return "5"
	}
	panic("unknown GOARCH: " + runtime.GOARCH)
}
