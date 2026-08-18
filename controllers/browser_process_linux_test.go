//go:build browser && linux

package controllers

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type browserProcess struct {
	pid  int
	pgid int
}

func TestBrowserProcessTreeCleanup(t *testing.T) {
	command := exec.Command("sh", "-c", "sleep 30 & setsid sleep 30 & wait")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatalf("start process-tree fixture: %v", err)
	}

	var processes []browserProcess
	fixtureReady := false
	fixtureDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(fixtureDeadline) {
		processes = captureBrowserProcessTree(command.Process.Pid)
		groups := make(map[int]struct{})
		for _, process := range processes {
			groups[process.pgid] = struct{}{}
		}
		if len(processes) >= 3 && len(groups) >= 2 {
			fixtureReady = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !fixtureReady {
		_ = terminateBrowserProcessTree(command.Process.Pid)
		_ = command.Wait()
		t.Fatalf("process-tree fixture did not start all descendants")
	}

	if err := terminateBrowserProcessTree(command.Process.Pid); err != nil {
		t.Fatalf("terminate process-tree fixture: %v", err)
	}
	_ = command.Wait()

	if groups := liveBrowserProcessGroups(processes); len(groups) != 0 {
		t.Fatalf("process-tree cleanup left %d process groups running", len(groups))
	}
}

func terminateBrowserProcessTree(rootPID int) error {
	processes := captureBrowserProcessTree(rootPID)
	rootPGID, err := syscall.Getpgid(rootPID)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	if err != nil {
		return err
	}

	if err := syscall.Kill(-rootPGID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}

	graceDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(graceDeadline) {
		if len(liveBrowserProcessGroups(processes)) == 0 {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	for pgid := range liveBrowserProcessGroups(processes) {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
	}

	hardKillDeadline := time.Now().Add(time.Second)
	for time.Now().Before(hardKillDeadline) {
		if len(liveBrowserProcessGroups(processes)) == 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("browser process cleanup timed out")
}

func captureBrowserProcessTree(rootPID int) []browserProcess {
	processes := make([]browserProcess, 0)
	queue := []int{rootPID}
	seen := make(map[int]struct{})

	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if _, ok := seen[pid]; ok {
			continue
		}
		seen[pid] = struct{}{}

		if pgid, err := syscall.Getpgid(pid); err == nil {
			processes = append(processes, browserProcess{pid: pid, pgid: pgid})
		}

		childrenPath := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
		children, err := os.ReadFile(childrenPath)
		if err != nil {
			continue
		}
		for _, child := range strings.Fields(string(children)) {
			childPID, err := strconv.Atoi(child)
			if err == nil {
				queue = append(queue, childPID)
			}
		}
	}
	return processes
}

func liveBrowserProcessGroups(processes []browserProcess) map[int]struct{} {
	groups := make(map[int]struct{})
	for _, process := range processes {
		if !browserProcessIsRunning(process.pid) {
			continue
		}
		pgid, err := syscall.Getpgid(process.pid)
		if err == nil && pgid == process.pgid {
			groups[pgid] = struct{}{}
		}
	}
	return groups
}

func browserProcessIsRunning(pid int) bool {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	endOfName := strings.LastIndex(string(stat), ") ")
	if endOfName < 0 || endOfName+2 >= len(stat) {
		return false
	}
	return stat[endOfName+2] != 'Z'
}
