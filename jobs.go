package main

import (
	"fmt"
	"os"
	"syscall"
)

type jobState int

const (
	jobRunning jobState = iota
	jobStopped
	jobDone
)

func (s jobState) String() string {
	switch s {
	case jobRunning:
		return "Running"
	case jobStopped:
		return "Stopped"
	case jobDone:
		return "Done"
	default:
		return "Unknown"
	}
}

type job struct {
	id    int      // job number [1], [2], ...
	pgid  int      // process group ID
	pids  []int    // all PIDs in the pipeline
	cmd   string   // original command text for display
	state jobState
}

func (s *shellState) addJob(pgid int, pids []int, cmd string, state jobState) *job {
	s.nextJobID++
	j := &job{
		id:    s.nextJobID,
		pgid:  pgid,
		pids:  pids,
		cmd:   cmd,
		state: state,
	}
	s.jobs = append(s.jobs, j)
	return j
}

func (s *shellState) removeJob(id int) {
	for i, j := range s.jobs {
		if j.id == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			return
		}
	}
}

func (s *shellState) findJob(id int) *job {
	for _, j := range s.jobs {
		if j.id == id {
			return j
		}
	}
	return nil
}

// currentJob returns the most recent stopped job, or failing that,
// the most recent running job. Used by bare fg/bg.
func (s *shellState) currentJob() *job {
	// Prefer stopped jobs (most recent first).
	for i := len(s.jobs) - 1; i >= 0; i-- {
		if s.jobs[i].state == jobStopped {
			return s.jobs[i]
		}
	}
	// Fall back to running jobs.
	for i := len(s.jobs) - 1; i >= 0; i-- {
		if s.jobs[i].state == jobRunning {
			return s.jobs[i]
		}
	}
	return nil
}

// reapJobs does a non-blocking wait to collect any finished background
// jobs. Called before each prompt to report completed jobs.
func (s *shellState) reapJobs() {
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG|syscall.WUNTRACED, nil)
		if pid <= 0 || err != nil {
			break
		}

		for _, j := range s.jobs {
			if j.state == jobDone {
				continue
			}
			for _, p := range j.pids {
				if p == pid {
					if ws.Stopped() {
						j.state = jobStopped
					} else {
						j.state = jobDone
					}
					break
				}
			}
		}
	}

	// Print and remove done jobs.
	var remaining []*job
	for _, j := range s.jobs {
		if j.state == jobDone {
			fmt.Fprintf(os.Stderr, "[%d]+  Done                    %s\n", j.id, j.cmd)
		} else {
			remaining = append(remaining, j)
		}
	}
	s.jobs = remaining
}
