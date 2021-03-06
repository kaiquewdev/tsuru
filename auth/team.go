// Copyright 2013 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package auth

import (
	"fmt"
	"github.com/globocom/tsuru/db"
	"github.com/globocom/tsuru/log"
	"labix.org/v2/mgo/bson"
	"sync"
)

type Team struct {
	Name  string `bson:"_id"`
	Users []string
}

func (t *Team) ContainsUser(u *User) bool {
	for _, user := range t.Users {
		if u.Email == user {
			return true
		}
	}
	return false
}

func (t *Team) AddUser(u *User) error {
	if t.ContainsUser(u) {
		return fmt.Errorf("User %s is already in the team %s.", u.Email, t.Name)
	}
	t.Users = append(t.Users, u.Email)
	return nil
}

func (t *Team) RemoveUser(u *User) error {
	index := -1
	for i, user := range t.Users {
		if u.Email == user {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("User %s is not in the team %s.", u.Email, t.Name)
	}
	copy(t.Users[index:], t.Users[index+1:])
	t.Users = t.Users[:len(t.Users)-1]
	return nil
}

func GetTeamsNames(teams []Team) []string {
	tn := make([]string, len(teams))
	for i, t := range teams {
		tn[i] = t.Name
	}
	return tn
}

func CheckUserAccess(teamNames []string, u *User) bool {
	q := bson.M{"_id": bson.M{"$in": teamNames}}
	var teams []Team
	conn, err := db.Conn()
	if err != nil {
		log.Printf("Failed to connect to the database: %s", err)
		return false
	}
	defer conn.Close()
	conn.Teams().Find(q).All(&teams)
	var wg sync.WaitGroup
	found := make(chan bool)
	for _, team := range teams {
		wg.Add(1)
		go func(t Team) {
			if t.ContainsUser(u) {
				found <- true
			}
			wg.Done()
		}(team)
	}
	go func() {
		wg.Wait()
		found <- false
	}()
	return <-found
}
