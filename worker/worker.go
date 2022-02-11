package worker

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/wesleybits/gangpile/protocol"
)

type Worker struct {
	scriptfile string
	context context.Context
	stop context.CancelFunc
	runs []protocol.RunResults
	name string
	socket string
	listener net.Listener
	lock *sync.Mutex
	running bool
}

func NewUDSWorker(name string) (worker *Worker, err error) {
	socketname := fmt.Sprintf("/tmp/%s.sock", name)
	if _, err = os.Stat(socketname); !os.IsNotExist(err) {
		err = fmt.Errorf("Cannot map to already used socket: %s", socketname)
		return
	}
	var listener net.Listener
	if listener, err = net.Listen("unix", socketname); err != nil {
		err = fmt.Errorf("Cannot open UDS: %s", err.Error())
		return
	}
	worker = &Worker{
		name: name,
		socket: socketname,
		listener: listener,
		lock: new(sync.Mutex),
		running: false,
	}
	err = nil
	return
}

func NewTCPWorker(name, port string) (worker *Worker, err error) {
	var listener net.Listener
	if listener, err = net.Listen("tcp", fmt.Sprintf(":%s", port)); err != nil {
		err = fmt.Errorf("Cannot open TCP on %s: %s", port, err.Error())
	}
	worker = &Worker{
		name: name,
		socket: port,
		listener: listener,
		lock: new(sync.Mutex),
		running: false,
	}
	err = nil
	return
}

func RunRPC(w *Worker) {
	rpc.Register(w)
	rpc.HandleHTTP()
	http.Serve(w.listener, nil)
}

// run support:
//  - buff_2_lines: turn a byte buffer into a slice of lines
//  - work_loop: the meat of this program, keep slapping a script until told to stop
func buff_2_lines(buff *bytes.Buffer) (lines []string) {
	lines = []string{}
	scanner := bufio.NewScanner(buff)
	for buff.Len() > 0 {
		lines = append(lines, scanner.Text())
	}
	return
}

func (w *Worker) work_loop() {
	for true {
		select {
		case <-w.context.Done():
			return
		default:
			w.lock.Lock()
			run := protocol.RunResults{}
			run.Scriptfile = w.scriptfile
			cmd := exec.Command(w.scriptfile)
			cmd_out := bytes.NewBuffer([]byte{})
			cmd_err := bytes.NewBuffer([]byte{})
			cmd.Stdout = cmd_out
			cmd.Stderr = cmd_err
			run.StartTime = time.Now()
			if err := cmd.Run(); err != nil {
				run.Stderr = []string{err.Error()}
				run.Stdout = []string{}
				run.ExitCode = int(protocol.SpecialErrorDEADBEAT)
				run.EndTime = time.Now()
				continue
			}
			if err := cmd.Wait(); err != nil {
				run.Stderr = []string{err.Error()}
				run.Stdout = []string{}
				run.ExitCode = int(protocol.SpecialErrorDEADGHOST)
				run.EndTime = time.Now()
				continue
			}
			run.EndTime = time.Now()
			run.Stdout = buff_2_lines(cmd_out)
			run.Stderr = buff_2_lines(cmd_err)
			run.ExitCode = cmd.ProcessState.ExitCode()
			w.runs = append(w.runs, run)
			w.lock.Unlock()
		}
	}
}

// start the worker loop if not already started
func (w *Worker) Start(script string, ok *bool) {
	if _, err := os.Stat(script); os.IsNotExist(err) {
		fmt.Printf("Script not found: %s\n", script)
		*ok = false
		return
	}
	if w.running {
		fmt.Printf("Already running\n")
		*ok = false
		return
	}
	w.scriptfile = script
	w.context, w.stop = context.WithCancel(context.Background())
	w.runs = []protocol.RunResults{}
	go w.work_loop()
	w.running = true
	*ok = true
	return
}

// stop the worker
func (w *Worker) Stop(code int, result *protocol.Report) {
	if !w.running {
		return
	}
	w.stop()
	w.lock.Lock()
	result.WorkerName = w.name
	result.Runs = w.runs
	w.listener.Close()
	go func() {
		time.Sleep(1 * time.Second)
		os.Exit(0)
	}()
}

// get a worker report, and clear it's cache of prior runs
func (w *Worker) Report(code int, result *protocol.Report) {
	if !w.running {
		return
	}
	w.lock.Lock()
	result.WorkerName = w.name
	result.Runs = w.runs
	w.runs = []protocol.RunResults{}
	w.lock.Unlock()
}
