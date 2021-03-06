// Copyright 2013 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package local

import (
	"github.com/globocom/commandmocker"
	"github.com/globocom/config"
	"github.com/globocom/tsuru/fs/testing"
	"io/ioutil"
	"launchpad.net/gocheck"
)

func (s *S) TestAddRoute(c *gocheck.C) {
	config.Set("local:domain", "andrewzito.com")
	config.Set("local:routes-path", "testdata")
	rfs := &testing.RecordingFs{}
	fsystem = rfs
	defer func() {
		fsystem = nil
	}()
	err := AddRoute("name", "127.0.0.1")
	c.Assert(err, gocheck.IsNil)
	file, _ := rfs.Open("testdata/name")
	data, err := ioutil.ReadAll(file)
	c.Assert(err, gocheck.IsNil)
	expected := `server {
	listen 80;
	name.andrewzito.com;
	location / {
		proxy_pass http://127.0.0.1;
	}
}`
	c.Assert(string(data), gocheck.Equals, expected)
}

func (s *S) TestRestartRouter(c *gocheck.C) {
	tmpdir, err := commandmocker.Add("sudo", "$*")
	c.Assert(err, gocheck.IsNil)
	defer commandmocker.Remove(tmpdir)
	err = RestartRouter()
	c.Assert(err, gocheck.IsNil)
	c.Assert(commandmocker.Ran(tmpdir), gocheck.Equals, true)
	expected := "service nginx restart"
	c.Assert(commandmocker.Output(tmpdir), gocheck.Equals, expected)
}
