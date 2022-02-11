package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"

	_ "github.com/mattn/sqlite-3"
	"github.com/wesleybits/gangpile/protocol"
)

func is_digit(c rune) bool {
	return c >= '0' && c <= '9'
}

func worker_idx(workername string) (idx int, err error) {
	const (
		CHAR = iota
		DIGIT
	)

	number := ""
	state := CHAR
	for _, c := range workername {
		switch state {
		case CHAR:
			if is_digit(c) {
				state = DIGIT
				number += string(c)
			}
		case DIGIT:
			if is_digit(c) {
				number += string(c)
			} else {
				state = CHAR
				number = ""
			}
		}
	}
	idx, err = strconv.Atoi(number)
	return
}

type Database struct {
	dbfile string
	db *sql.DB
	ctx context.Context
	records chan *protocol.Report
	stop context.CancelFunc
	wait_group sync.WaitGroup
}

func (db *Database) init() (err error) {
	_, err = db.db.Exec(`
create table Runs (
	runid uint not null,
	worker uint not null,
	script text not null,
	started datetime not null,
	ended datetime not null,
	primary key (runid, worker)
)`)
	if err != nil {
		fmt.Printf("DB ERROR: %s\n", err.Error())
		return
	}

	_, err = db.db.Exec(`
create table StdoutLogs (
	lineid uint not null,
	runid uint not null,
	worker uint not null,
	line text not null,
	primary key (lineid, runid, worker)
)`)
	if err != nil {
		fmt.Printf("DB ERROR: %s\n", err.Error())
		return
	}

	_, err = db.db.Exec(`
create table StderrLogs (
	lineid uint not null,
	runid uint not null,
	worker uint not null,
	line text not null,
	primary key (lineid, runid, worker)
)`)
	if err != nil {
		fmt.Printf("DB ERROR: %s\n", err.Error())
	}
	return
}

func log_error(prefix string, e error) {
	if e != nil {
		fmt.Printf("%s: %s", prefix, e.Error())
	}
}

func (db *Database) work_loop() {
	for true {
		select {
		case report := <- db.records:
			worker, err := worker_idx(report.WorkerName)
			if err != nil {
				fmt.Printf("DB report error (worker index from name): %s\n", err.Error())
				continue
			}
			max_runids, err := db.db.Query("select max(runid) from Runs where worker = ?", worker)
			last_runid := 0
			for max_runids.Next() {
				max_runids.Scan(&last_runid)
			}
			max_runids.Close()

			for i, run := range report.Runs {
				runid := last_runid + i + 1
				started := run.StartTime
				ended := run.EndTime
				script := run.Scriptfile
				_, err := db.db.Exec(
					"insert into Runs (runid, worker, script, started, ended) values (?, ?, ?, ?, ?)",
					runid,
					worker,
					script,
					started,
					ended)
				log_error("ERROR on 'insert Runs': ", err)

				insert_stdout, err := db.db.Prepare("insert into StdoutLogs (lineid, runid, worker, line) values (?, ?, ?, ?)")
				log_error("ERROR preparing 'insert StdoutLogs'", err)

				insert_stderr, err := db.db.Prepare("insert into StderrLogs (lineid, runid, worker, line) values (?, ?, ?, ?)")
				log_error("ERROR preparing 'insert StderrLogs'", err)

				for i, line := range run.Stdout {
					_, err := insert_stdout.Exec(i, runid, worker, line)
					log_error("ERROR running 'insert StdoutLogs': ", err)
				}

				for i, line := range run.Stderr {
					_, err := insert_stderr.Exec(i, runid, worker, line)
					log_error("ERROR on 'insert StdoutLogs': ", err)
				}
				insert_stdout.Close()
				insert_stderr.Close()
			}
		case <-db.ctx.Done():
			db.db.Close()
			return
		}
	}
}

func NewDatabase(file string) (db *Database, err error) {
	if _, _err := os.Stat(file); !os.IsNotExist(_err) {
		err = fmt.Errorf("File already exists; please move or remove.")
		return
	}
	if _db, _err := sql.Open("sqlite3", file); _err != nil {
		err = fmt.Errorf("Error while creating db file: %s", _err.Error())
		return
	} else {
		db = &Database{
			db: _db,
			dbfile: file,
			records: make(chan *protocol.Report, 10),
		}
		db.ctx, db.stop = context.WithCancel(context.Background())
		db.wait_group.Add(1)
		go func() {
			db.work_loop()
			db.wait_group.Done()
		}
		return
	}
}

func (db *Database) SaveReport(r *protocol.Report) {
	db.records <- r
}

func (db *Database) Close() {
	db.stop()
	db.wait_group.Wait()
}
