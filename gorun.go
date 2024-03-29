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
	"bytes"
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
		args = append(args, ".")
	}

	if args[0] == "-h" || args[0] == "help" || args[0] == "-help" || args[0] == "--help" {
		fmt.Fprintln(os.Stderr, "usage: gorun <source file> [...]")
		os.Exit(1)
	}

	err := Run(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "An uncaught error has occurred.")
	os.Exit(1)
}

// Run compiles and links the Go source file on args[0] and
// runs it with arguments args[1:].
func Run(args []string) error {
	sourcefile := args[0]
	runBaseDir, runFile, runCmdDir, err := RunFilePaths(sourcefile)
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

	rstat, err := os.Stat(runFile)
	switch {
	case err != nil:
		compile = true
	case rstat.Mode()&(os.ModeDir|os.ModeSymlink|os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0:
		return errors.New("not a file: " + runFile)
	case rstat.ModTime().Before(sstat.ModTime()) || rstat.Mode().Perm()&0700 != 0700:
		compile = true
	default:
		// We have spare cycles. Maybe remove old files.
		if err := os.Chtimes(runBaseDir, now, now); err == nil {
			cDirErr := CleanDir(runBaseDir, now)
			if cDirErr != nil {
				return cDirErr
			}
		}
	}

	for retry := 3; retry > 0; retry-- {
		if compile {
			err := Compile(sourcefile, runFile, runCmdDir)
			if err != nil {
				return err
			}
			// If sourcefile was changed, will be updated on next run.
			err = os.Chtimes(runFile, sstat.ModTime(), sstat.ModTime())
			if err != nil {
				return err
			}
		}

		err = syscall.Exec(runFile, args, os.Environ())
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

func getSection(content []byte, sectionName string) (section []byte) {
	start := "// " + sectionName + " >>>"
	end := "// <<< " + sectionName
	startIdx := bytes.Index(content, []byte(start))
	if startIdx >= 0 {
		idxEnd := bytes.Index(content, []byte(end))
		if idxEnd > startIdx {
			goMod := string(content[startIdx+len(start) : idxEnd])
			goMod = strings.ReplaceAll(goMod, "\n// ", "\n")
			goMod = strings.ReplaceAll(goMod, "\n//", "\n")
			return []byte(goMod)
		}
	}
	return []byte("")
}

func writeFileFromComments(content []byte, sectionName string, file string) (written bool, err error) {
	// Write go.mod and go.sum files from inside the comments
	section := getSection(content, sectionName)
	if len(section) > 0 {
		err = ioutil.WriteFile(file, section, 0600)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Failed to write "+sectionName+" to "+file)
			return
		}
		written = true
	}
	return
}

// Compile compiles and links sourcefile and atomically renames the
// resulting binary to runfile.
func Compile(sourcefile, runFile string, runCmdDir string) (err error) {
	pid := strconv.Itoa(os.Getpid())

	err = os.MkdirAll(runCmdDir, 0700)
	if err != nil {
		return err
	}
	var writtenSource bool
	content, _ := ioutil.ReadFile(sourcefile)
	if len(content) > 2 && content[0] == '#' && content[1] == '!' {
		content[0] = '/'
		content[1] = '/'
		writtenSource = true
	}

	// TODO in an ideal world to protect against potential races on multiple runs, we'd
	// include <pid> in the name, but go build wants it called go.mod, so we could put
	// it all in its separate directory and copy over when done.
	// Write a go.mod file from inside the comments
	modFile := runCmdDir + "go.mod"
	os.Remove(modFile)
	writtenMod, err := writeFileFromComments(content, "go.mod", modFile)
	if err != nil {
		return
	}

	// Write a go.sum file from inside the comments
	sumFile := runCmdDir + "go.sum"
	os.Remove(sumFile)
	writtenSum, err := writeFileFromComments(content, "go.sum", sumFile)
	if err != nil {
		return
	}

	// only copy the source file to the runCmdDir if something needs to be changed about it
	// or if it has an embedded go.mod or go.sum
	execDir := ""
	if writtenSource || writtenMod || writtenSum {
		sourcefile = runFile + "." + pid + ".go"
		err := ioutil.WriteFile(sourcefile, content, 0600)
		if err != nil {
			return err
		}
		defer os.Remove(sourcefile)
		execDir = runCmdDir
	}

	// use the default environment before adding our overrides
	var env []string
	section := getSection(content, "go.env")
	if len(section) > 0 {
		env = os.Environ()
		env = append(env, strings.Split(string(section), "\n")...)
	}

	gotool := filepath.Join(runtime.GOROOT(), "bin", "go")

	if _, err := os.Stat(gotool); err != nil {
		if gotool, err = exec.LookPath("go"); err != nil {
			return errors.New("can't find go tool")
		}
	}

	out := runFile + "." + pid

	err = Exec(execDir, env, []string{gotool, "build", "-o", out, sourcefile})
	if err != nil {
		return err
	}
	return os.Rename(out, runFile)
}

// Exec runs args[0] with args[1:] arguments and passes through
// stdout and stderr.
func Exec(dir string, env []string, args []string) error {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if dir != "" {
		cmd.Dir = dir
	}

	if cmd.Env != nil {
		cmd.Env = append(cmd.Env, env...)
	} else {
		cmd.Env = env
	}
	err := cmd.Run()

	base := filepath.Base(args[0])
	if err != nil {
		return errors.New("failed to run " + base + ": " + err.Error())
	}
	return nil
}

// RunFilePaths returns various paths involved in building and caching a gorun binary.
//
// Each cached gorun binary lives under its own directory to allow separate go.mod
// and go.sum files to be embedded and extracted from the source file.
//
// Note that runBaseDir contains directories for each gorun binary.
// runFile is the full path to the cached gorun binary
// runCmdDir is the directory inside runBaseDir where runFile lives.
func RunFilePaths(sourcefile string) (runBaseDir, runFile string, runCmdDir string, err error) {
	runBaseDir, err = RunBaseDir()
	if err != nil {
		return "", "", "", err
	}
	sourcefile, err = filepath.Abs(sourcefile)
	if err != nil {
		return "", "", "", err
	}
	sourcefile, err = filepath.EvalSymlinks(sourcefile)
	if err != nil {
		return "", "", "", err
	}
	pathElements := strings.Split(sourcefile, string(filepath.Separator))
	baseFileName := pathElements[len(pathElements)-1]
	runFile = strings.Replace(sourcefile, "_", "__", -1)
	runFile = strings.Replace(runFile, string(filepath.Separator), "ROOT_", 1)
	runFile = strings.Replace(runFile, string(filepath.Separator), "_", -1)
	runCmdDir = filepath.Join(runBaseDir, runFile) + string(filepath.Separator)

	runFile = runCmdDir
	runFile += baseFileName + ".gorun"

	return
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
func RunBaseDir() (rundir string, err error) {
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
}

const CleanFileDelay = time.Hour * 24 * 7

// CleanDir removes binary files under rundir in case they were not
// accessed for more than CleanFileDelay nanoseconds.  A last-cleaned
// marker file is created so that the next verification is only done
// after CleanFileDelay nanoseconds.
func CleanDir(runBaseDir string, now time.Time) error {
	cleanedfile := filepath.Join(runBaseDir, "last-cleaned")
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
	d, err := os.Open(runBaseDir)
	if err != nil {
		return err
	}
	infos, err := d.Readdir(-1)
	if err != nil {
		return err
	}
	for _, info := range infos {
		atim := atime(info)
		access := time.Unix(int64(atim.Sec), int64(atim.Nsec))
		if access.Before(cleanLine) {
			os.RemoveAll(filepath.Join(runBaseDir, info.Name()))
		}
	}
	return nil
}

/*
// TheChar returns the magic architecture char.
// We should find out if we need this or not.
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
*/
