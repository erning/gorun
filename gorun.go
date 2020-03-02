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
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func Usage() {
	fmt.Fprintf(flag.CommandLine.Output(),
		flag.CommandLine.Name()+`: Compile and run a go program directly.

Options can be provided via GORUN_ARGS environment variable, or on the command line

`)
	fmt.Fprintf(flag.CommandLine.Output(), "%s [options] <sourceFile.go>:\n", flag.CommandLine.Name())
	flag.PrintDefaults()
}

func main() {
	flag.Usage = Usage

	// gather up all args, command line and GORUN_ARGS in to one array
	gorunArgsEnv, _ := os.LookupEnv("GORUN_ARGS")
	gorunArgs := strings.Fields(gorunArgsEnv)
	args := append(gorunArgs, os.Args[1:]...)

	diffArg := flag.Bool("diff", false, "show diff between embedded comments and filesystem go.mod/go.sum")
	embedArg := flag.Bool("embed", false, "embed filesystem go.mod/go.sum as comments in source file")
	extractArg := flag.Bool("extract", false, "extract the comments to filesystem go.mod/go.sum")
	embedIgnoreRegex := flag.String("embedIgnoreRegex", "^/(bin|sbin|usr|opt|root)/(.*)", "Do not embed if the filepath of source file matches this golang regex")
	noRun := flag.Bool("noRun", false, "don't attempt to compile or run")
	flag.CommandLine.Parse(args)

	if len(args) == flag.NFlag() {
		Usage()
		os.Exit(1)
	}

	sourceFile, err := realPath(flag.Args()[0])
	if err != nil {
		fmt.Printf("Failed to find source file %q", err.Error())
		return
	}

	if *diffArg {
		err = diff(sourceFile)
	} else if *extractArg {
		err = extract(sourceFile)
	} else if *embedArg {
		err = embed(sourceFile, sourceFile, *embedIgnoreRegex)
	}

	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to run embedded command %q\n", err.Error())
		os.Exit(1)
	}
	if *noRun {
		if !*diffArg && !*extractArg && !*embedArg {
			_, _ = fmt.Fprintln(os.Stderr, "-noRun specified, but nothing else specified to do. Exit.")
		}
		os.Exit(0)
	}

	err = Run(flag.Args())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error: "+err.Error())
		os.Exit(1)
	}

	panic("unreachable")
}

func loadFile(filename string) (found bool, content []byte, err error) {
	_, err = os.Stat(filename)
	if err != nil {
		return false, nil, nil // no error if file not there
	}
	content, err = ioutil.ReadFile(filename)
	if err != nil {
		return // error if file there but can't be read
	}
	found = true
	// get rid of extra new lines and whitespace
	content = bytes.TrimSpace(content)
	content = bytes.Replace(content, []byte("\n\n"), []byte("\n"), -1)
	return
}

func diffBytes(content []byte, dir string, sectionName string) (diff string, err error) {
	section := getSection(content, sectionName)
	section = bytes.TrimSpace(section)
	section = bytes.Replace(section, []byte("\n\n"), []byte("\n"), -1)

	foundOnDisc, sectionFromFile, err := loadFile(filepath.Join(dir, sectionName))
	if err != nil { // file exists but unable to read
		return
	}
	if !foundOnDisc && len(section) == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "OK: section %q not embedded or on disc\n", sectionName)
		return "", nil
	}
	if !foundOnDisc {
		_, _ = fmt.Fprintf(os.Stderr, "WARN: embedded %q exists but nothing on disc\n", sectionName)
		return "embeddedExists", nil
	}
	if len(section) == 0 && len(sectionFromFile) > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "WARN: on disc %q exists but embedded doesn't\n", sectionName)
		return "discExists", nil
	}
	if bytes.Equal(sectionFromFile, section) {
		_, _ = fmt.Fprintf(os.Stderr, "OK: embedded %q exists and same as on disc\n", sectionName)
		return "", nil
	}
	_, _ = fmt.Fprintf(os.Stderr, "WARN: embedded %q exists and different to on disc\n", sectionName)
	return "diff", nil
}

func diff(sourceFile string) (err error) {
	content, err := ioutil.ReadFile(sourceFile)
	if err != nil {
		return
	}
	diff1, err := diffBytes(content, filepath.Dir(sourceFile), "go.mod")
	if err != nil {
		return
	}
	diff2, err := diffBytes(content, filepath.Dir(sourceFile), "go.sum")
	if err != nil {
		return
	}

	if diff1 != "" || diff2 != "" {
		_, _ = fmt.Fprintln(os.Stderr, "Diffs found\n")
		os.Exit(1)
	}
	return
}

func extractToFile(content []byte, dir string, section string) (err error) {
	file := filepath.Join(dir, section)
	_, err = writeFileFromComments(content, section, file)
	return
}

func extract(sourceFile string) (err error) {
	content, err := ioutil.ReadFile(sourceFile)
	if err != nil {
		return
	}
	err = extractToFile(content, filepath.Dir(sourceFile), "go.sum")
	if err != nil {
		return
	}
	err = extractToFile(content, filepath.Dir(sourceFile), "go.mod")
	return
}

func commentSection(content []byte, header string, trailer string) (commented []byte) {
	commented = bytes.ReplaceAll(content, []byte("\n"), []byte("\n// "))
	commented = append(commented, []byte("\n")...)
	commented = append([]byte("// "), commented...)
	commented = append([]byte(header), commented...)
	commented = append(commented, []byte(trailer)...)
	return
}

func header(section string) (header string) {
	return "// " + section + " >>>\n"
}

func trailer(section string) (trailer string) {
	return "// <<< " + section + "\n"
}

func embed(sourceFile string, destFile string, embedIgnoreRegex string) (err error) {
	matched, err := regexp.Match(embedIgnoreRegex, []byte(sourceFile))
	if matched {
		return nil
	}

	content, err := ioutil.ReadFile(sourceFile)
	if err != nil {
		return
	}
	foundSumOnDisc, sumContent, err := loadFile(filepath.Join(filepath.Dir(sourceFile), "go.sum"))
	if err != nil {
		return
	}
	foundModOnDisc, modContent, err := loadFile(filepath.Join(filepath.Dir(sourceFile), "go.mod"))
	if err != nil {
		return
	}

	// let's only delete an embedded section if there is a section file (e.g. go.sum) on disc alongside
	startSumIdx := 0
	if foundSumOnDisc {
		startSumIdx, content = removeSection(content, "go.sum")
		idx := 0
		if startSumIdx >= 0 {
			idx = startSumIdx
		}
		var contentStart, contentTrailer []byte
		contentStart = append(contentStart, content[0:idx]...)
		contentTrailer = append(contentTrailer, content[idx:len(content)]...)
		content = append(contentStart, commentSection(sumContent, header("go.sum"), trailer("go.sum"))...)
		content = append(content, contentTrailer...)
	}

	if foundModOnDisc {
		var startModIdx int
		startModIdx, content = removeSection(content, "go.mod")
		idx := 0
		if startModIdx >= 0 {
			idx = startModIdx
		}
		var contentStart, contentTrailer []byte
		contentStart = append(contentStart, content[0:idx]...)
		// only add a newline between sections go.sum and go.mod sections if we've added a new go.sum section,
		// otherwise leave it as the user had it
		if foundSumOnDisc && startSumIdx < 0 {
			contentTrailer = append(contentTrailer, []byte("\n")...)
		}
		contentTrailer = append(contentTrailer, content[idx:len(content)]...)
		content = append(contentStart, commentSection(modContent, header("go.mod"), trailer("go.mod"))...)
		content = append(content, contentTrailer...)
	}

	err = ioutil.WriteFile(destFile, content, 0600)
	return
}

func realPath(sourceFile string) (realPath string, err error) {
	sourceFile, err = filepath.Abs(sourceFile)
	if err != nil {
		return "", err
	}
	realPath, err = filepath.EvalSymlinks(sourceFile)
	if err != nil {
		return "", err
	}
	return
}

// Run compiles and links the Go source file on args[0] and
// runs it with arguments args[1:].
func Run(args []string) error {
	sourceFile := args[0]
	runBaseDir, runFile, runCmdDir, err := RunFilePaths(sourceFile)
	if err != nil {
		return err
	}

	compile := false

	// Now must be called before Stat of sourceFile below,
	// so that changing the file between Stat and Chtimes still
	// causes the file to be updated on the next run.
	now := time.Now()

	sstat, err := os.Stat(sourceFile)
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
			CleanDir(runBaseDir, now)
		}
	}

	for retry := 3; retry > 0; retry-- {
		if compile {
			err := Compile(sourceFile, runFile, runCmdDir)
			if err != nil {
				return err
			}
			// If sourceFile was changed, will be updated on next run.
			os.Chtimes(runFile, sstat.ModTime(), sstat.ModTime())
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

func sectionIndexes(content []byte, sectionName string) (found bool, startIdx int, startInnerIdx int, endInnerIdx int, endIdx int) {
	start := header(sectionName)
	end := trailer(sectionName)
	startIdx = bytes.Index(content, []byte(start))
	startInnerIdx = startIdx + len(start)
	endInnerIdx = bytes.Index(content, []byte(end))
	endIdx = endInnerIdx + len(end)
	found = startIdx >= 0 && endIdx > startIdx
	return
}

func getSection(content []byte, sectionName string) (section []byte) {
	found, _, startInnerIdx, endInnerIdx, _ := sectionIndexes(content, sectionName)
	if found {
		goMod := string(content[startInnerIdx:endInnerIdx])
		goMod = strings.ReplaceAll(goMod, "// ", "")
		goMod = strings.ReplaceAll(goMod, "//", "")
		return []byte(goMod)
	}
	return []byte("")
}

func removeSection(content []byte, sectionName string) (startIdx int, newContent []byte) {
	found, startIdx, _, _, endIdx := sectionIndexes(content, sectionName)
	if found {
		newContent = content[0:startIdx]
		newContent = append(newContent, content[endIdx:len(content)]...)
	} else {
		newContent = content
	}
	return
}

func writeFileFromComments(content []byte, sectionName string, file string) (written bool, err error) {
	// Write a go.mod file from inside the comments
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

// Compile compiles and links sourceFile and atomically renames the
// resulting binary to runfile.
func Compile(sourceFile, runFile string, runCmdDir string) (err error) {
	pid := strconv.Itoa(os.Getpid())

	err = os.MkdirAll(runCmdDir, 0700)
	if err != nil {
		return err
	}
	var writtenSource bool
	content, err := ioutil.ReadFile(sourceFile)
	if err != nil {
		return
	}
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

	// TODO as go.mod
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
		sourceFile = runFile + "." + pid + ".go"
		ioutil.WriteFile(sourceFile, content, 0600)
		defer os.Remove(sourceFile)
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

	err = Exec(execDir, env, []string{gotool, "build", "-o", out, sourceFile})
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
func RunFilePaths(sourceFile string) (runBaseDir, runFile string, runCmdDir string, err error) {
	runBaseDir, err = RunBaseDir()
	sourceFile, err = realPath(sourceFile)
	if err != nil {
		return "", "", "", err
	}
	pathElements := strings.Split(sourceFile, string(filepath.Separator))
	baseFileName := pathElements[len(pathElements)-1]
	runFile = strings.Replace(sourceFile, "_", "__", -1)
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

// RunDir returns the directory where binary files generated should be put.
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
	panic("unreachable")
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
	for _, info := range infos {
		atim := atime(info)
		access := time.Unix(int64(atim.Sec), int64(atim.Nsec))
		if access.Before(cleanLine) {
			os.RemoveAll(filepath.Join(runBaseDir, info.Name()))
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
