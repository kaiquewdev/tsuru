// Copyright 2013 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package auth

import (
	"code.google.com/p/go.crypto/bcrypt"
	"code.google.com/p/go.crypto/pbkdf2"
	"crypto/sha512"
	stderrors "errors"
	"fmt"
	"github.com/globocom/config"
	"github.com/globocom/tsuru/db"
	"github.com/globocom/tsuru/errors"
	"github.com/globocom/tsuru/validation"
	"labix.org/v2/mgo/bson"
	"time"
)

const (
	defaultExpiration = 7 * 24 * time.Hour
	emailError        = "Invalid email."
	passwordError     = "Password length should be least 6 characters and at most 50 characters."
	passwordMinLen    = 6
	passwordMaxLen    = 50
)

var salt, tokenKey string
var tokenExpire time.Duration
var cost int

func loadConfig() error {
	if salt == "" && tokenKey == "" {
		var err error
		if salt, err = config.GetString("auth:salt"); err != nil {
			return stderrors.New(`Setting "auth:salt" is undefined.`)
		}
		if iface, err := config.Get("auth:token-expire-days"); err == nil {
			day := int64(iface.(int))
			tokenExpire = time.Duration(day * 24 * int64(time.Hour))
		} else {
			tokenExpire = defaultExpiration
		}
		if tokenKey, err = config.GetString("auth:token-key"); err != nil {
			return stderrors.New(`Setting "auth:token-key" is undefined.`)
		}
		if cost, err = config.GetInt("auth:hash-cost"); err != nil {
			return stderrors.New(`Setting "auth:hash-cost" is undefined.`)
		}
		if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
			return fmt.Errorf("Invalid value for setting %q: it must be between %d and %d.", "auth:hash-cost", bcrypt.MinCost, bcrypt.MaxCost)
		}
	}
	return nil
}

// hashPassword hashes a password using the old method (PBKDF2 + SHA512).
//
// BUG(fss): this function is deprecated, it's here for the migration phase
// (whenever a user login with the old hash, the new hash will be generated).
func hashPassword(password string) string {
	err := loadConfig()
	if err != nil {
		panic(err)
	}
	salt := []byte(salt)
	return fmt.Sprintf("%x", pbkdf2.Key([]byte(password), salt, 4096, len(salt)*8, sha512.New))
}

type Key struct {
	Name    string
	Content string
}

type User struct {
	Email    string
	Password string
	Keys     []Key
}

func GetUserByEmail(email string) (*User, error) {
	if !validation.ValidateEmail(email) {
		return nil, &errors.ValidationError{Message: emailError}
	}
	var u User
	conn, err := db.Conn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	err = conn.Users().Find(bson.M{"email": email}).One(&u)
	if err != nil {
		return nil, stderrors.New("User not found")
	}
	return &u, nil
}

func (u *User) Create() error {
	conn, err := db.Conn()
	if err != nil {
		return err
	}
	defer conn.Close()
	u.HashPassword()
	return conn.Users().Insert(u)
}

func (u *User) Update() error {
	conn, err := db.Conn()
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Users().Update(bson.M{"email": u.Email}, u)
}

func (u *User) HashPassword() {
	if passwd, err := bcrypt.GenerateFromPassword([]byte(u.Password), cost); err == nil {
		u.Password = string(passwd)
	}
}

func (u *User) CheckPassword(password string) error {
	if !validation.ValidateLength(password, passwordMinLen, passwordMaxLen) {
		return &errors.ValidationError{Message: passwordError}
	}
	// BUG(fss): this is temporary code, for a migration phase in the
	// hashing algorithm. In the future we should just use
	// bcrypt.CompareHashAndPassword, and drop the old hash checking and
	// update stuff.
	if bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)) == nil {
		return nil
	}
	hashedPassword := hashPassword(password)
	if u.Password == hashedPassword {
		if bcryptPassword, err := bcrypt.GenerateFromPassword([]byte(password), cost); err == nil {
			u.Password = string(bcryptPassword)
			u.Update()
		}
		return nil
	}
	return AuthenticationFailure{}
}

func (u *User) CreateToken(password string) (*Token, error) {
	if u.Email == "" {
		return nil, stderrors.New("User does not have an email")
	}
	if err := u.CheckPassword(password); err != nil {
		return nil, err
	}
	conn, err := db.Conn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	t, err := newUserToken(u)
	if err != nil {
		return nil, err
	}
	err = conn.Tokens().Insert(t)
	return t, err
}

// Teams returns a slice containing all teams that the user is member of.
func (u *User) Teams() (teams []Team, err error) {
	conn, err := db.Conn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	err = conn.Teams().Find(bson.M{"users": u.Email}).All(&teams)
	return
}

func (u *User) FindKey(key Key) (Key, int) {
	for i, k := range u.Keys {
		if k.Content == key.Content {
			return k, i
		}
	}
	return Key{}, -1
}

func (u *User) HasKey(key Key) bool {
	_, index := u.FindKey(key)
	return index > -1
}

func (u *User) AddKey(key Key) error {
	u.Keys = append(u.Keys, key)
	return nil
}

func (u *User) RemoveKey(key Key) error {
	_, index := u.FindKey(key)
	if index < 0 {
		return stderrors.New("Key not found")
	}
	copy(u.Keys[index:], u.Keys[index+1:])
	u.Keys = u.Keys[:len(u.Keys)-1]
	return nil
}

func (u *User) IsAdmin() bool {
	adminTeamName, err := config.GetString("admin-team")
	if err != nil {
		return false
	}
	teams, err := u.Teams()
	if err != nil {
		return false
	}
	for _, t := range teams {
		if t.Name == adminTeamName {
			return true
		}
	}
	return false
}

func (u *User) AllowedApps() ([]string, error) {
	conn, err := db.Conn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var alwdApps []map[string]string
	teams, err := u.Teams()
	if err != nil {
		return []string{}, err
	}
	teamNames := GetTeamsNames(teams)
	q := bson.M{"teams": bson.M{"$in": teamNames}}
	if err := conn.Apps().Find(q).Select(bson.M{"name": 1}).All(&alwdApps); err != nil {
		return []string{}, err
	}
	appNames := make([]string, len(alwdApps))
	for i, v := range alwdApps {
		appNames[i] = v["name"]
	}
	return appNames, nil
}

func (u *User) AllowedAppsByTeam(team string) ([]string, error) {
	conn, err := db.Conn()
	if err != nil {
		return []string{}, err
	}
	defer conn.Close()
	alwdApps := []map[string]string{}
	if err := conn.Apps().Find(bson.M{"teams": bson.M{"$in": []string{team}}}).Select(bson.M{"name": 1}).All(&alwdApps); err != nil {
		return []string{}, err
	}
	appNames := make([]string, len(alwdApps))
	for i, v := range alwdApps {
		appNames[i] = v["name"]
	}
	return appNames, nil
}

type AuthenticationFailure struct{}

func (AuthenticationFailure) Error() string {
	return "Authentication failed, wrong password."
}
