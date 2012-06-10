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
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}

	panic("unreachable")
}

// Run compiles and links the Go source file on args[0] and
// runs it with arguments args[1:].
func Run(args []string) error {
	sourcefile := args[0]
	rundir, runfile, err := RunFile(sourcefile)
	if err != nil {
		return err
	}

	compile := false

	// Now must be called before Stat of sourcefile below,
	// so that changing the file between Stat and Chtimes still
	// causes the file to be updated on the next run.
	now := time.Now()

	sstat, err := os.Stat(sourcefile)
	if err != nil {
		return err
	}

	rstat, err := os.Stat(runfile)
	switch {
	case err != nil:
		compile = true
	case rstat.Mode()&(os.ModeDir|os.ModeSymlink|os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0:
		return errors.New("not a file: " + runfile)
	case rstat.ModTime().Before(sstat.ModTime()) || rstat.Mode().Perm()&0700 != 0700:
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
			os.Chtimes(runfile, sstat.ModTime(), sstat.ModTime())
		}

		err = syscall.Exec(runfile, args, os.Environ())
		if os.IsNotExist(err) {
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
func Compile(sourcefile, runfile string) (err error) {
	pid := strconv.Itoa(os.Getpid())

	content, err := ioutil.ReadFile(sourcefile)
	if len(content) > 2 && content[0] == '#' && content[1] == '!' {
		content[0] = '/'
		content[1] = '/'
		sourcefile = runfile + "." + pid + ".go"
		ioutil.WriteFile(sourcefile, content, 0600)
		defer os.Remove(sourcefile)
	}

	gotool := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(gotool); err != nil {
		if gotool, err = exec.LookPath("go"); err != nil {
			return errors.New("can't find go tool")
		}
	}
	n := TheChar()
	gcout := runfile + "." + pid + "." + n
	ldout := runfile + "." + pid
	err = Exec([]string{gotool, "tool", n+"g", "-o", gcout, sourcefile})
	if err != nil {
		return err
	}
	defer os.Remove(gcout)
	err = Exec([]string{gotool, "tool", n+"l", "-o", ldout, gcout})
	if err != nil {
		return err
	}
	return os.Rename(ldout, runfile)
}

// Exec runs args[0] with args[1:] arguments and passes through
// stdout and stderr.
func Exec(args []string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	base := filepath.Base(args[0])
	if err != nil {
		return errors.New("failed to run " + base + ": " + err.Error())
	}
	return nil
}

// RunFile returns the directory and file location where the binary generated
// for sourcefile should be put.  In case the directory does not yet exist, it
// will be created by RunDir.
func RunFile(sourcefile string) (rundir, runfile string, err error) {
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

func sysStat(stat os.FileInfo) *syscall.Stat_t {
	return stat.Sys().(*syscall.Stat_t)
}

func canWrite(stat os.FileInfo, euid, egid int) bool {
	perm := stat.Mode().Perm()
	sstat := sysStat(stat)
	return perm&02 != 0 || perm&020 != 0 && uint32(egid) == sstat.Gid || perm&0200 != 0 && uint32(euid) == sstat.Uid
}

// RunDir returns the directory where binary files generates should be put.
// In case a safe directory isn't found, one will be created.
func RunDir() (rundir string, err error) {
	tempdir := os.TempDir()
	euid := os.Geteuid()
	stat, err := os.Stat(tempdir)
	if err != nil || !stat.IsDir() || !canWrite(stat, euid, os.Getegid()) {
		return "", errors.New("can't write on directory: " + tempdir)
	}
	hostname, err := os.Hostname()
	if err != nil {
		return "", errors.New("can't get hostname: " + err.Error())
	}
	prefix := "gorun-" + hostname + "-" + strconv.Itoa(euid)
	suffix := runtime.GOOS + "_" + runtime.GOARCH
	prefixi := prefix
	var i uint64
	for {
		rundir = filepath.Join(tempdir, prefixi, suffix)

		// A directory is only considered safe if the owner matches the
		// user running the script and its permissions prevent someone
		// else from writing on it.
		stat, err := os.Stat(rundir)
		if err == nil && stat.IsDir() && stat.Mode().Perm() == 0700 && sysStat(stat).Uid == uint32(euid) {
			return rundir, nil
		}
		if os.IsNotExist(err) {
			err := os.MkdirAll(rundir, 0700)
			if err == nil {
				return rundir, nil
			}
		}
		i++
		prefixi = prefix + "-" + strconv.FormatUint(i, 10)
	}
	panic("unreachable")
}

const CleanFileDelay = time.Hour * 24 * 7

// CleanDir removes binary files under rundir in case they were not
// accessed for more than CleanFileDelay nanoseconds.  A last-cleaned
// marker file is created so that the next verification is only done
// after CleanFileDelay nanoseconds.
func CleanDir(rundir string, now time.Time) error {
	cleanedfile := filepath.Join(rundir, "last-cleaned")
	cleanLine := now.Add(-CleanFileDelay)
	if info, err := os.Stat(cleanedfile); err == nil && info.ModTime().After(cleanLine) {
		// It's been cleaned recently.
		return nil
	}
	f, err := os.Create(cleanedfile)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte(now.Format(time.RFC3339)))
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
	for _, info := range infos {
		atim := sysStat(info).Atim
		access := time.Unix(int64(atim.Sec), int64(atim.Nsec))
		if access.Before(cleanLine) {
			os.Remove(filepath.Join(rundir, info.Name()))
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
