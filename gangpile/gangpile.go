package main

import (
	"context"
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wesleybits/gangpile/database"
	"github.com/wesleybits/gangpile/protocol"
)

type LineFixer struct {
	underlying io.Writer
	newline    bool
	prefix     string
}

func fix(prefix string, underlying io.Writer) *LineFixer {
	return &LineFixer{underlying, true, prefix}
}

func (lf *LineFixer) Write(bytes []byte) (n int, err error) {
	const (
		INLINE = iota
		NEWLINE
	)

	chunk := []byte{}
	state := INLINE
	if lf.newline {
		state = NEWLINE
	}
	for _, b := range bytes {
		switch state {
		case INLINE:
			chunk = append(chunk, b)
			if b == '\n' {
				state = NEWLINE
			}
		case NEWLINE:
			chunk = append(chunk, []byte(lf.prefix)...)
			chunk = append(chunk, b)
			state = INLINE
		}
	}
	n = len(bytes)
	_, err = lf.underlying.Write(chunk)
	return
}

type WorkerClient struct {
	server *rpc.Client
	proc   *exec.Cmd
}

func new_local_client(name string) (client WorkerClient, err error) {
	sockname := fmt.Sprintf("/tmp/%s.sock", name)
	if rclient, e := rpc.DialHTTP("unix", sockname); e != nil {
		err = e
	} else {
		client = WorkerClient{rclient, nil}
	}
	return
}

func new_remote_client(host, port string) (client WorkerClient, err error) {
	address := fmt.Sprintf("%s:%s", host, port)
	if rclient, e := rpc.DialHTTP("tcp", address); e != nil {
		err = e
	} else {
		client = WorkerClient{rclient, nil}
	}
	return
}

func (w WorkerClient) start(script string) (ok bool) {
	w.server.Call("Worker.Start", script, &ok)
	return
}

func (w WorkerClient) stop() (result *protocol.Report) {
	result = new(protocol.Report)
	w.server.Call("Worker.Stop", 0, result)
	w.proc.Wait()
	return
}

func (w WorkerClient) report() (result *protocol.Report) {
	result = new(protocol.Report)
	w.server.Call("Worker.Report", 1, result)
	return
}

func (w WorkerClient) kill() (err error) {
	w.server.Close()
	err = w.proc.Process.Kill()
	w.proc.Wait()
	return
}

func go_local(num int, script string) (clients []WorkerClient) {
	var err error
	var client WorkerClient
	var proc *exec.Cmd

	dir := filepath.Dir(script)
	name := script[len(dir):]

	clients = []WorkerClient{}

	for i := 0; i < num; i++ {
		workername := fmt.Sprintf("%s_%d", name, i)
		prefix := fmt.Sprintf("%s: ", workername)
		sockname := fmt.Sprintf("/tmp/%s.sock", name)
		proc = exec.Command("./localrunner", workername)
		proc.Stdout = fix(prefix, os.Stdout)
		proc.Stderr = fix(prefix, os.Stderr)
		if err = proc.Run(); err != nil {
			fmt.Printf("Could not start worker %d: %s\n", i, err.Error())
			continue
		}
		for true {
			if _, err = os.Stat(sockname); os.IsNotExist(err) {
				time.Sleep(10 * time.Microsecond)
			} else {
				break
			}
		}
		if client, err = new_local_client(workername); err != nil {
			proc.Process.Kill()
			fmt.Printf("Issue connecting to worker %d at %s: %s\n", i, sockname, err.Error())
			continue
		}
		client.proc = proc
		clients = append(clients, client)
	}
	return
}

func main() {
	if len(os.Args) < 3 {
		fmt.Printf("Local usage:  gangpile local <N WORKERS> <SCRIPT PATH>\n")
		fmt.Printf("Remote usage: gangpile remote <WORKER CONFIG> <SCRIPT PATH>\n")
		os.Exit(1)
	}

	var clients []WorkerClient
	var script string
	switch os.Args[1] {
	case "local":
		if num_workers, err := strconv.Atoi(os.Args[2]); err != nil {
			fmt.Printf("Third argument must be an integer\n")
			os.Exit(1)
		} else if num_workers < 1 {
			fmt.Printf("Number of workers needs to be 1 or greater\n")
			os.Exit(2)
		} else if scriptstat, err := os.Stat(os.Args[3]); os.IsNotExist(err) {
			fmt.Printf("Script given does not exist!\n")
			os.Exit(4)
		} else if scriptstat.Mode().IsDir() {
			fmt.Printf("Script path must include the actual filename of the script\n")
			os.Exit(5)
		} else if scriptstat.Mode()&0o111 == 0 {
			fmt.Printf("Script must be executable (preferrably by you!)\n")
			os.Exit(6)
		} else if !scriptstat.Mode().IsRegular() {
			fmt.Printf("Please specify the path to the physical file, no links!\n")
			os.Exit(7)
		} else {
			script = os.Args[3]
			clients = go_local(num_workers, script)
		}
	case "remote":
		fmt.Printf("Remote worker infection not implemented yet...\n")
		os.Exit(1)
	default:
		fmt.Printf("Unknown mode: %s\n", os.Args[1])
		os.Exit(1)
	}

	ctx, stop_pollers := context.WithCancel(context.Background())
	db, err := database.NewDatabase("runs_results.db")
	if err != nil {
		fmt.Printf("Initialization error (starting database): %s\n", err.Error())
		os.Exit(10)
	}

	for i, client := range clients {
		if !client.start(script) {
			fmt.Printf("Worker %d failed to start; bad script path or already running.\n", i)
		}
	}

	go func() {
		for true {
			time.Sleep(1 * time.Second)
			select {
			case <-ctx.Done():
				for i, client := range clients {
					fmt.Printf("Stopping worker %d\n", i)
					db.SaveReport(client.stop())
				}
				return
			default:
				for _, client := range clients {
					db.SaveReport(client.report())
				}
			}
		}
	}()

	syssigs := make(chan os.Signal)
	signal.Notify(syssigs, os.Interrupt)
	signal.Notify(syssigs, os.Kill)

	<-syssigs

	stop_pollers()
	db.Close()

	fmt.Printf("Done\n")
}
