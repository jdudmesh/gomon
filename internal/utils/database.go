package utils

// gomon is a simple command line tool that watches your files and automatically restarts the application when it detects any changes in the working directory.
// Copyright (C) 2023 John Dudmesh

// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

import (
	"fmt"
	"os"
	"path"

	"github.com/jdudmesh/gomon/internal/config"
	"github.com/jdudmesh/gomon/internal/notification"
	"github.com/jmoiron/sqlx"
)

type Database struct {
	db *sqlx.DB
}

func NewDatabase(config config.Config) (*Database, error) {
	dataPath := path.Join(config.RootDirectory, "./.gomon")
	_, err := os.Stat(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.Mkdir(dataPath, 0755)
			if err != nil {
				return nil, fmt.Errorf("creating .gomon directory: %w", err)
			}
		} else {
			return nil, fmt.Errorf("checking for .gomon directory: %w", err)
		}
	}

	db, err := sqlx.Connect("sqlite3", path.Join(dataPath, "./gomon.db"))
	if err != nil {
		return nil, fmt.Errorf("connecting to sqlite: %w", err)
	}

	_, err = db.Exec(schema)
	if err != nil {
		return nil, fmt.Errorf("creating db schema: %w", err)
	}

	return &Database{db: db}, nil
}

var schema = `
CREATE TABLE IF NOT EXISTS notifs (
	id TEXT PRIMARY KEY,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	child_process_id TEXT NOT NULL,
	event_type TEXT NOT NULL,
	event_data TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS notifications_child_process_id ON notifs(child_process_id);
CREATE INDEX IF NOT EXISTS notifications_event_type ON notifs(event_type);
`

func (d *Database) Close() error {
	return d.db.Close()
}

func (d *Database) Notify(n notification.Notification) error {
	_, err := d.db.NamedExec(`
		INSERT INTO notifs (id, created_at, child_process_id, event_type, event_data)
		VALUES (:id, :created_at, :child_process_id, :event_type, :event_data)
	`, n)
	return err
}

func (d *Database) FindRuns() ([]*notification.Notification, error) {
	runs := []*notification.Notification{}
	err := d.db.Select(&runs, "SELECT * FROM notifs WHERE event_type = ? ORDER BY created_at DESC LIMIT 100;", notification.NotificationTypeStartup)
	if err != nil {
		return nil, fmt.Errorf("getting runs: %w", err)
	}

	return runs, nil
}

func (d *Database) FindNotifications(runID, stm, filter string) ([][]*notification.Notification, error) {
	var err error
	notifs := [][]*notification.Notification{}

	if runID == "" {
		err = d.db.Get(&runID, "SELECT child_process_id FROM notifs WHERE event_type = ? ORDER BY created_at DESC LIMIT 1;", notification.NotificationTypeStartup)
		if err != nil {
			return nil, fmt.Errorf("getting last run id: %w", err)
		}
	}

	if runID != "" {
		params := map[string]interface{}{}
		sql := "SELECT * FROM notifs WHERE "
		if runID == "all" {
			sql += "1 = 1 " // dummy clause
		} else {
			params["child_process_id"] = runID
			sql += "child_process_id = :child_process_id "
		}
		if !(stm == "" || stm == "all") {
			sql += " AND event_type = :event_type "
			params["event_type"] = stm
		}
		if filter != "" {
			sql += " AND event_data LIKE :event_data "
			params["event_data"] = "%" + filter + "%"
		}
		sql += " ORDER BY child_process_id ASC, created_at ASC limit 1000;"

		res, err := d.db.NamedQuery(sql, params)
		if err != nil {
			return nil, fmt.Errorf("querying notifications: %w", err)
		}
		defer res.Close()

		var lastRunID string = ""
		for res.Next() {
			ev := new(notification.Notification)
			err = res.StructScan(ev)
			if err != nil {
				return nil, fmt.Errorf("scanning notification: %w", err)
			}
			if ev.ChildProccessID != "" {
				if lastRunID != ev.ChildProccessID {
					notifs = append(notifs, []*notification.Notification{})
					lastRunID = ev.ChildProccessID
				}
				notifs[len(notifs)-1] = append(notifs[len(notifs)-1], ev)
			}
		}
	}

	return notifs, nil
}
