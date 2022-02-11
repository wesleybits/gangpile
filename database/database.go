package database

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/wesleybits/gangpile/protocol"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const logs_file = "run_logs.db"

//////////////////
// The DB Model //
//////////////////

type Run struct {
	gorm.Model
	Worker     string
	Scriptfile string
	StartedAt  time.Time
	EndedAt    time.Time
	StdoutLog  []StdoutLine `gorm:"foreignKey:RunsId"`
	StderrLog  []StderrLine `gorm:"foriengKey:RunsId"`
}

type StdoutLine struct {
	gorm.Model
	RunId int64
	Line  string
}

type StderrLine struct {
	gorm.Model
	RunId int64
	Line  string
}

/////////////////////////////////////
// High-level Logging DB Interface //
/////////////////////////////////////

type Database struct {
	dbfile     string
	db         *gorm.DB
	ctx        context.Context
	records    chan *protocol.Report
	wait_group sync.WaitGroup
}

func (db *Database) init() (err error) {
	if db.db, err = gorm.Open(sqlite.Open(logs_file), &gorm.Config{}); err != nil {
		return
	}

	// GORM comes with a migration tool!
	db.db.AutoMigrate(&StdoutLine{}, &StderrLine{}, &Run{})

	return
}

func log_error(prefix string, e error) {
	if e != nil {
		fmt.Printf("%s: %s", prefix, e.Error())
	}
}

// Because SQLite is hilariously bad a concurrency management, we'll only be
// inserting records one batch at a time.
func (db *Database) work_loop() {
	for true {
		select {
		case report := <-db.records:
			for _, run := range report.Runs {
				run_model := Run{
					Worker:     report.WorkerName,
					Scriptfile: run.Scriptfile,
					StartedAt:  run.StartTime,
					EndedAt:    run.EndTime,
					StdoutLog:  []StdoutLine{},
					StderrLog:  []StderrLine{},
				}
				for _, stdout := range run.Stdout {
					run_model.StdoutLog = append(run_model.StdoutLog, StdoutLine{Line: stdout})
				}
				for _, stderr := range run.Stderr {
					run_model.StderrLog = append(run_model.StderrLog, StderrLine{Line: stderr})
				}
				db.db.Create(&run_model)
			}
		case <-db.ctx.Done():
			db.wait_group.Done()
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// You'll only need to call this once. needs a cancel-able context to know when
// to stop running. Once canceled, you'll need to call (*Database).Drain() to
// wait until all the inserts are finished.
func NewDatabase(ctx context.Context, file string) (db *Database, err error) {
	if _, _err := os.Stat(file); !os.IsNotExist(_err) {
		err = fmt.Errorf("File already exists; please move or remove.")
	} else {
		db = &Database{
			dbfile:  file,
			records: make(chan *protocol.Report, 10),
			ctx:     ctx,
		}
		db.init()
		db.wait_group.Add(1)
		go db.work_loop()
	}
	return
}

// Nonblocking missive to save a report, because we in a hurry and actually
// performing the insert is a sombody-else problem (see (*Database).work_loop
// for what that "somebody-else" is).
func (db *Database) SaveReport(r *protocol.Report) {
	db.records <- r
}

// call after canceling the given context. this waits until the DB is finished
// with all it's queued inserts.
func (db *Database) Drain() {
	db.wait_group.Wait()
}
