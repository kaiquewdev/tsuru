// Copyright 2013 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package app

import (
	"bytes"
	"encoding/json"
	stderr "errors"
	"fmt"
	"github.com/globocom/config"
	"github.com/globocom/tsuru/app/bind"
	"github.com/globocom/tsuru/auth"
	"github.com/globocom/tsuru/errors"
	"github.com/globocom/tsuru/log"
	"github.com/globocom/tsuru/provision"
	"github.com/globocom/tsuru/queue"
	"github.com/globocom/tsuru/repository"
	"github.com/globocom/tsuru/service"
	"labix.org/v2/mgo/bson"
	"launchpad.net/gocheck"
	stdlog "log"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func (s *S) TestGet(c *gocheck.C) {
	newApp := App{Name: "myApp", Framework: "Django"}
	err := s.conn.Apps().Insert(newApp)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": newApp.Name})
	newApp.Env = map[string]bind.EnvVar{}
	err = s.conn.Apps().Update(bson.M{"name": newApp.Name}, &newApp)
	c.Assert(err, gocheck.IsNil)
	myApp := App{Name: "myApp"}
	err = myApp.Get()
	c.Assert(err, gocheck.IsNil)
	c.Assert(myApp.Name, gocheck.Equals, newApp.Name)
}

func (s *S) TestForceDestroy(c *gocheck.C) {
	h := testHandler{}
	ts := s.t.StartGandalfTestServer(&h)
	defer ts.Close()
	a := App{
		Name:      "ritual",
		Framework: "ruby",
		Units:     []Unit{{Name: "duvido", Machine: 3}},
	}
	err := s.conn.Apps().Insert(&a)
	c.Assert(err, gocheck.IsNil)
	a.Get()
	err = ForceDestroy(&a)
	c.Assert(err, gocheck.IsNil)
	err = a.Get()
	c.Assert(err, gocheck.NotNil)
	qt, err := s.conn.Apps().Find(bson.M{"name": a.Name}).Count()
	c.Assert(err, gocheck.IsNil)
	c.Assert(qt, gocheck.Equals, 0)
	c.Assert(s.provisioner.FindApp(&a), gocheck.Equals, -1)
}
func (s *S) TestDestroy(c *gocheck.C) {
	h := testHandler{}
	ts := s.t.StartGandalfTestServer(&h)
	defer ts.Close()
	a := App{
		Name:      "ritual",
		Framework: "ruby",
		Units: []Unit{
			{
				Name:    "duvido",
				Machine: 3,
			},
		},
	}
	err := CreateApp(&a, 1, []auth.Team{s.team})
	c.Assert(err, gocheck.IsNil)
	a.Get()
	token := a.Env["TSURU_APP_TOKEN"].Value
	err = ForceDestroy(&a)
	c.Assert(err, gocheck.IsNil)
	err = a.Get()
	c.Assert(err, gocheck.NotNil)
	qt, err := s.conn.Apps().Find(bson.M{"name": a.Name}).Count()
	c.Assert(err, gocheck.IsNil)
	c.Assert(qt, gocheck.Equals, 0)
	c.Assert(s.provisioner.FindApp(&a), gocheck.Equals, -1)
	msg, err := aqueue().Get(1e6)
	c.Assert(err, gocheck.IsNil)
	c.Assert(msg.Args, gocheck.DeepEquals, []string{a.Name})
	msg.Delete()
	_, err = auth.GetToken(token)
	c.Assert(err, gocheck.NotNil)
	c.Assert(err.Error(), gocheck.Equals, "Token not found")
}

func (s *S) TestDestroyWithoutBucketSupport(c *gocheck.C) {
	config.Unset("bucket-support")
	defer config.Set("bucket-support", true)
	h := testHandler{}
	ts := s.t.StartGandalfTestServer(&h)
	defer ts.Close()
	a := App{
		Name:      "blinded",
		Framework: "evergrey",
		Units:     []Unit{{Name: "duvido", Machine: 3}},
	}
	err := CreateApp(&a, 1, []auth.Team{s.team})
	c.Assert(err, gocheck.IsNil)
	a.Get()
	err = ForceDestroy(&a)
	c.Assert(err, gocheck.IsNil)
	err = a.Get()
	c.Assert(err, gocheck.NotNil)
	qt, err := s.conn.Apps().Find(bson.M{"name": a.Name}).Count()
	c.Assert(err, gocheck.IsNil)
	c.Assert(qt, gocheck.Equals, 0)
	c.Assert(s.provisioner.FindApp(&a), gocheck.Equals, -1)
	msg, err := aqueue().Get(1e6)
	c.Assert(err, gocheck.IsNil)
	c.Assert(msg.Args, gocheck.DeepEquals, []string{a.Name})
	msg.Delete()
}

func (s *S) TestDestroyWithoutUnits(c *gocheck.C) {
	h := testHandler{}
	ts := s.t.StartGandalfTestServer(&h)
	defer ts.Close()
	app := App{Name: "x4"}
	err := CreateApp(&app, 1, []auth.Team{s.team})
	c.Assert(err, gocheck.IsNil)
	defer s.provisioner.Destroy(&app)
	app.Get()
	err = ForceDestroy(&app)
	c.Assert(err, gocheck.IsNil)
	msg, err := aqueue().Get(1e6)
	c.Assert(err, gocheck.IsNil)
	c.Assert(msg.Args, gocheck.DeepEquals, []string{app.Name})
	msg.Delete()
}

func (s *S) TestCreateApp(c *gocheck.C) {
	patchRandomReader()
	defer unpatchRandomReader()
	ts := s.t.StartGandalfTestServer(&testHandler{})
	defer ts.Close()
	a := App{
		Name:      "appname",
		Framework: "django",
		Units:     []Unit{{Machine: 3}},
	}
	expectedHost := "localhost"
	config.Set("host", expectedHost)

	err := CreateApp(&a, 3, []auth.Team{s.team})
	c.Assert(err, gocheck.IsNil)
	defer ForceDestroy(&a)
	err = a.Get()
	c.Assert(err, gocheck.IsNil)
	var retrievedApp App
	err = s.conn.Apps().Find(bson.M{"name": a.Name}).One(&retrievedApp)
	c.Assert(err, gocheck.IsNil)
	c.Assert(retrievedApp.Name, gocheck.Equals, a.Name)
	c.Assert(retrievedApp.Framework, gocheck.Equals, a.Framework)
	c.Assert(retrievedApp.Teams, gocheck.DeepEquals, []string{s.team.Name})
	env := a.InstanceEnv(s3InstanceName)
	c.Assert(env["TSURU_S3_ENDPOINT"].Value, gocheck.Equals, s.t.S3Server.URL())
	c.Assert(env["TSURU_S3_ENDPOINT"].Public, gocheck.Equals, false)
	c.Assert(env["TSURU_S3_LOCATIONCONSTRAINT"].Value, gocheck.Equals, "true")
	c.Assert(env["TSURU_S3_LOCATIONCONSTRAINT"].Public, gocheck.Equals, false)
	e, ok := env["TSURU_S3_ACCESS_KEY_ID"]
	c.Assert(ok, gocheck.Equals, true)
	c.Assert(e.Public, gocheck.Equals, false)
	e, ok = env["TSURU_S3_SECRET_KEY"]
	c.Assert(ok, gocheck.Equals, true)
	c.Assert(e.Public, gocheck.Equals, false)
	c.Assert(env["TSURU_S3_BUCKET"].Value, gocheck.HasLen, maxBucketSize)
	c.Assert(env["TSURU_S3_BUCKET"].Value, gocheck.Equals, "appnamee3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3e3")
	c.Assert(env["TSURU_S3_BUCKET"].Public, gocheck.Equals, false)
	env = a.InstanceEnv("")
	c.Assert(env["TSURU_APPNAME"].Value, gocheck.Equals, a.Name)
	c.Assert(env["TSURU_APPNAME"].Public, gocheck.Equals, false)
	c.Assert(env["TSURU_HOST"].Value, gocheck.Equals, expectedHost)
	c.Assert(env["TSURU_HOST"].Public, gocheck.Equals, false)
	expectedMessage := queue.Message{
		Action: regenerateApprc,
		Args:   []string{a.Name},
	}
	message, err := aqueue().Get(1e6)
	c.Assert(err, gocheck.IsNil)
	defer message.Delete()
	c.Assert(message.Action, gocheck.Equals, expectedMessage.Action)
	c.Assert(message.Args, gocheck.DeepEquals, expectedMessage.Args)
	c.Assert(s.provisioner.GetUnits(&a), gocheck.HasLen, 3)
}

func (s *S) TestCreateWithoutBucketSupport(c *gocheck.C) {
	config.Unset("bucket-support")
	defer config.Set("bucket-support", true)
	h := testHandler{}
	ts := s.t.StartGandalfTestServer(&h)
	defer ts.Close()
	a := App{
		Name:      "sorry",
		Framework: "evergrey",
		Units:     []Unit{{Machine: 3}},
	}
	expectedHost := "localhost"
	config.Set("host", expectedHost)
	err := CreateApp(&a, 3, []auth.Team{s.team})
	c.Assert(err, gocheck.IsNil)
	defer ForceDestroy(&a)
	err = a.Get()
	c.Assert(err, gocheck.IsNil)
	var retrievedApp App
	err = s.conn.Apps().Find(bson.M{"name": a.Name}).One(&retrievedApp)
	c.Assert(err, gocheck.IsNil)
	c.Assert(retrievedApp.Name, gocheck.Equals, a.Name)
	c.Assert(retrievedApp.Framework, gocheck.Equals, a.Framework)
	c.Assert(retrievedApp.Teams, gocheck.DeepEquals, []string{s.team.Name})
	env := a.InstanceEnv(s3InstanceName)
	c.Assert(env, gocheck.DeepEquals, map[string]bind.EnvVar{})
	message, err := aqueue().Get(1e6)
	c.Assert(err, gocheck.IsNil)
	defer message.Delete()
	c.Assert(message.Action, gocheck.Equals, regenerateApprc)
	c.Assert(message.Args, gocheck.DeepEquals, []string{a.Name})
	c.Assert(s.provisioner.GetUnits(&a), gocheck.HasLen, 3)
}

func (s *S) TestCantCreateAppWithZeroUnits(c *gocheck.C) {
	a := App{Name: "paradisum"}
	err := CreateApp(&a, 0, []auth.Team{s.team})
	c.Assert(err, gocheck.NotNil)
	c.Assert(err.Error(), gocheck.Equals, "Cannot create app with 0 units.")
}

func (s *S) TestCantCreateAppWithoutTeams(c *gocheck.C) {
	input := [][]auth.Team{nil, {}}
	a := App{Name: "beyond"}
	for _, t := range input {
		err := CreateApp(&a, 1, t)
		c.Check(err, gocheck.NotNil)
		_, ok := err.(NoTeamsError)
		c.Check(ok, gocheck.Equals, true)
	}
}

func (s *S) TestCantCreateTwoAppsWithTheSameName(c *gocheck.C) {
	err := s.conn.Apps().Insert(bson.M{"name": "appname"})
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": "appname"})
	a := App{Name: "appname"}
	err = CreateApp(&a, 1, []auth.Team{s.team})
	defer ForceDestroy(&a) // clean mess if test fail
	c.Assert(err, gocheck.NotNil)
	e, ok := err.(*appCreationError)
	c.Assert(ok, gocheck.Equals, true)
	c.Assert(e.app, gocheck.Equals, "appname")
	c.Assert(e.err, gocheck.NotNil)
	c.Assert(e.err.Error(), gocheck.Equals, "there is already an app with this name.")
}

func (s *S) TestCantCreateAppWithInvalidName(c *gocheck.C) {
	a := App{
		Name:      "1123app",
		Framework: "ruby",
	}
	err := CreateApp(&a, 1, []auth.Team{s.team})
	c.Assert(err, gocheck.NotNil)
	e, ok := err.(*errors.ValidationError)
	c.Assert(ok, gocheck.Equals, true)
	msg := "Invalid app name, your app should have at most 63 " +
		"characters, containing only lower case letters or numbers, " +
		"starting with a letter."
	c.Assert(e.Message, gocheck.Equals, msg)
}

func (s *S) TestDoesNotSaveTheAppInTheDatabaseIfProvisionerFail(c *gocheck.C) {
	h := testHandler{}
	ts := s.t.StartGandalfTestServer(&h)
	defer ts.Close()
	s.provisioner.PrepareFailure("Provision", stderr.New("exit status 1"))
	a := App{
		Name:      "theirapp",
		Framework: "ruby",
		Units:     []Unit{{Machine: 1}},
	}
	err := CreateApp(&a, 1, []auth.Team{s.team})
	defer ForceDestroy(&a) // clean mess if test fail
	c.Assert(err, gocheck.NotNil)
	expected := `Tsuru failed to create the app "theirapp": exit status 1`
	c.Assert(err.Error(), gocheck.Equals, expected)
	err = a.Get()
	c.Assert(err, gocheck.NotNil)
	msg, err := aqueue().Get(1e6)
	c.Assert(err, gocheck.IsNil)
	c.Assert(msg.Args, gocheck.DeepEquals, []string{a.Name})
	msg.Delete()
}

func (s *S) TestDeletesIAMCredentialsAndS3BucketIfProvisionerFail(c *gocheck.C) {
	h := testHandler{}
	ts := s.t.StartGandalfTestServer(&h)
	defer ts.Close()
	s.provisioner.PrepareFailure("Provision", stderr.New("exit status 1"))
	source := patchRandomReader()
	defer unpatchRandomReader()
	a := App{
		Name:      "theirapp",
		Framework: "ruby",
		Units:     []Unit{{Machine: 1}},
	}
	err := CreateApp(&a, 1, []auth.Team{s.team})
	defer ForceDestroy(&a) // clean mess if test fail
	c.Assert(err, gocheck.NotNil)
	iam := getIAMEndpoint()
	_, err = iam.GetUser("theirapp")
	c.Assert(err, gocheck.NotNil)
	s3 := getS3Endpoint()
	bucketName := fmt.Sprintf("%s%x", a.Name, source[:(maxBucketSize-len(a.Name)/2)])
	bucket := s3.Bucket(bucketName)
	_, err = bucket.Get("")
	c.Assert(err, gocheck.NotNil)
	msg, err := aqueue().Get(1e6)
	c.Assert(err, gocheck.IsNil)
	c.Assert(msg.Args, gocheck.DeepEquals, []string{a.Name})
	msg.Delete()
}

func (s *S) TestCreateAppCreatesRepositoryInGandalf(c *gocheck.C) {
	h := testHandler{}
	ts := s.t.StartGandalfTestServer(&h)
	defer ts.Close()
	a := App{
		Name:  "someapp",
		Teams: []string{s.team.Name},
		Units: []Unit{{Machine: 3}},
	}
	err := CreateApp(&a, 1, []auth.Team{s.team})
	c.Assert(err, gocheck.IsNil)
	err = a.Get()
	c.Assert(err, gocheck.IsNil)
	defer ForceDestroy(&a)
	c.Assert(h.url[0], gocheck.Equals, "/repository")
	c.Assert(h.method[0], gocheck.Equals, "POST")
	expected := fmt.Sprintf(`{"name":"someapp","users":["%s"],"ispublic":false}`, s.user.Email)
	c.Assert(string(h.body[0]), gocheck.Equals, expected)
	msg, err := aqueue().Get(1e6)
	c.Assert(err, gocheck.IsNil)
	c.Assert(msg.Args, gocheck.DeepEquals, []string{a.Name})
}

func (s *S) TestCreateAppDoesNotSaveTheAppWhenGandalfFailstoCreateTheRepository(c *gocheck.C) {
	ts := s.t.StartGandalfTestServer(&testBadHandler{msg: "could not create the repository"})
	defer ts.Close()
	a := App{Name: "otherapp"}
	err := CreateApp(&a, 1, []auth.Team{s.team})
	c.Assert(err, gocheck.NotNil)
	count, err := s.conn.Apps().Find(bson.M{"name": a.Name}).Count()
	c.Assert(err, gocheck.IsNil)
	c.Assert(count, gocheck.Equals, 0)
	msg, err := aqueue().Get(1e6)
	c.Assert(err, gocheck.IsNil)
	c.Assert(msg.Args, gocheck.DeepEquals, []string{a.Name})
}

func (s *S) TestAppendOrUpdate(c *gocheck.C) {
	a := App{
		Name:      "appName",
		Framework: "django",
	}
	u := Unit{Name: "i-00000zz8", Ip: "", Machine: 1}
	a.AddUnit(&u)
	c.Assert(len(a.Units), gocheck.Equals, 1)
	u = Unit{
		Name: "i-00000zz8",
		Ip:   "192.168.0.12",
	}
	a.AddUnit(&u)
	c.Assert(len(a.Units), gocheck.Equals, 1)
	c.Assert(a.Units[0], gocheck.DeepEquals, u)
}

func (s *S) TestAddUnits(c *gocheck.C) {
	app := App{Name: "warpaint", Framework: "python"}
	err := s.conn.Apps().Insert(app)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": app.Name})
	s.provisioner.Provision(&app)
	defer s.provisioner.Destroy(&app)
	otherApp := App{Name: "warpaint"}
	err = otherApp.AddUnits(5)
	c.Assert(err, gocheck.IsNil)
	units := s.provisioner.GetUnits(&app)
	c.Assert(units, gocheck.HasLen, 6)
	err = otherApp.AddUnits(2)
	c.Assert(err, gocheck.IsNil)
	units = s.provisioner.GetUnits(&app)
	c.Assert(units, gocheck.HasLen, 8)
	for _, unit := range units {
		c.Assert(unit.AppName, gocheck.Equals, app.Name)
	}
	err = app.Get()
	c.Assert(err, gocheck.IsNil)
	c.Assert(app.Units, gocheck.HasLen, 7)
	c.Assert(app.Framework, gocheck.Equals, "python")
	var expectedMessages MessageList
	for i, unit := range app.Units {
		expected := fmt.Sprintf("%s/%d", app.Name, i+1)
		c.Assert(unit.Name, gocheck.Equals, expected)
		messages := []queue.Message{
			{Action: RegenerateApprcAndStart, Args: []string{app.Name, unit.Name}},
			{Action: bindService, Args: []string{app.Name, unit.Name}},
		}
		expectedMessages = append(expectedMessages, messages...)
	}
	gotMessages := make(MessageList, expectedMessages.Len())
	for i := range expectedMessages {
		message, err := aqueue().Get(1e6)
		c.Assert(err, gocheck.IsNil)
		defer message.Delete()
		gotMessages[i] = queue.Message{
			Action: message.Action,
			Args:   message.Args,
		}
	}
	sort.Sort(expectedMessages)
	sort.Sort(gotMessages)
	c.Assert(gotMessages, gocheck.DeepEquals, expectedMessages)
}

func (s *S) TestAddZeroUnits(c *gocheck.C) {
	app := App{Name: "warpaint", Framework: "ruby"}
	err := app.AddUnits(0)
	c.Assert(err, gocheck.NotNil)
	c.Assert(err.Error(), gocheck.Equals, "Cannot add zero units.")
}

func (s *S) TestAddUnitsFailureInProvisioner(c *gocheck.C) {
	app := App{Name: "scars", Framework: "golang"}
	err := app.AddUnits(2)
	c.Assert(err, gocheck.NotNil)
	c.Assert(err.Error(), gocheck.Equals, "App is not provisioned.")
}

type hasUnitChecker struct{}

func (c *hasUnitChecker) Info() *gocheck.CheckerInfo {
	return &gocheck.CheckerInfo{Name: "HasUnit", Params: []string{"app", "unit"}}
}

func (c *hasUnitChecker) Check(params []interface{}, names []string) (bool, string) {
	a, ok := params[0].(*App)
	if !ok {
		return false, "first parameter should be a pointer to an app instance"
	}
	u, ok := params[1].(provision.AppUnit)
	if !ok {
		return false, "second parameter should be a pointer to an unit instance"
	}
	for _, unit := range a.ProvisionUnits() {
		if reflect.DeepEqual(unit, u) {
			return true, ""
		}
	}
	return false, ""
}

var HasUnit gocheck.Checker = &hasUnitChecker{}

func (s *S) TestRemoveUnitsPriority(c *gocheck.C) {
	units := []Unit{
		{Name: "ble/0", State: string(provision.StatusStarted)},
		{Name: "ble/1", State: string(provision.StatusDown)},
		{Name: "ble/2", State: string(provision.StatusCreating)},
		{Name: "ble/3", State: string(provision.StatusPending)},
		{Name: "ble/4", State: string(provision.StatusError)},
		{Name: "ble/5", State: string(provision.StatusInstalling)},
	}
	a := App{Name: "ble", Units: units}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	s.provisioner.Provision(&a)
	s.provisioner.AddUnits(&a, 6)
	un := a.ProvisionUnits()
	c.Assert(&a, HasUnit, un[0])
	c.Assert(&a, HasUnit, un[1])
	c.Assert(&a, HasUnit, un[2])
	c.Assert(&a, HasUnit, un[3])
	c.Assert(&a, HasUnit, un[4])
	c.Assert(&a, HasUnit, un[5])
	err = a.RemoveUnits(1)
	c.Assert(err, gocheck.IsNil)
	c.Assert(&a, gocheck.Not(HasUnit), un[4])
	err = a.RemoveUnits(1)
	c.Assert(err, gocheck.IsNil)
	c.Assert(&a, gocheck.Not(HasUnit), un[1])
	err = a.RemoveUnits(1)
	c.Assert(err, gocheck.IsNil)
	c.Assert(&a, gocheck.Not(HasUnit), un[3])
	err = a.RemoveUnits(1)
	c.Assert(err, gocheck.IsNil)
	c.Assert(&a, gocheck.Not(HasUnit), un[2])
	err = a.RemoveUnits(1)
	c.Assert(err, gocheck.IsNil)
	c.Assert(&a, gocheck.Not(HasUnit), un[5])
	c.Assert(&a, HasUnit, un[0])
	c.Assert(a.ProvisionUnits(), gocheck.HasLen, 1)
}

func (s *S) TestRemoveUnits(c *gocheck.C) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := atomic.LoadInt32(&calls)
		atomic.StoreInt32(&calls, v+1)
		w.WriteHeader(http.StatusNoContent)
	}))
	srvc := service.Service{Name: "mysql", Endpoint: map[string]string{"production": ts.URL}}
	err := srvc.Create()
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Services().Remove(bson.M{"_id": "mysql"})
	app := App{
		Name:      "chemistry",
		Framework: "python",
	}
	instance := service.ServiceInstance{
		Name:        "my-inst",
		ServiceName: "mysql",
		Teams:       []string{s.team.Name},
		Apps:        []string{app.Name},
	}
	instance.Create()
	defer s.conn.ServiceInstances().Remove(bson.M{"name": "my-inst"})
	err = s.conn.Apps().Insert(app)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": app.Name})
	c.Assert(err, gocheck.IsNil)
	s.provisioner.Provision(&app)
	defer s.provisioner.Destroy(&app)
	app.AddUnits(4)
	otherApp := App{Name: app.Name, Units: app.Units}
	err = otherApp.RemoveUnits(2)
	c.Assert(err, gocheck.IsNil)
	ts.Close()
	units := s.provisioner.GetUnits(&app)
	c.Assert(units, gocheck.HasLen, 3) // when you provision you already have one, so it's 4+1-2 (in provisioner, in app struct we have 2)
	c.Assert(units[0].Name, gocheck.Equals, "chemistry/0")
	c.Assert(units[1].Name, gocheck.Equals, "chemistry/3")
	c.Assert(units[2].Name, gocheck.Equals, "chemistry/4")
	err = app.Get()
	c.Assert(err, gocheck.IsNil)
	c.Assert(app.Framework, gocheck.Equals, "python")
	c.Assert(app.Units, gocheck.HasLen, 2)
	c.Assert(app.Units[0].Name, gocheck.Equals, "chemistry/3")
	c.Assert(app.Units[1].Name, gocheck.Equals, "chemistry/4")
	ok := make(chan int8)
	go func() {
		for _ = range time.Tick(1e3) {
			if atomic.LoadInt32(&calls) == 2 {
				ok <- 1
				return
			}
		}
	}()
	select {
	case <-ok:
	case <-time.After(2e9):
		c.Fatal("Did not call service endpoint twice.")
	}
}

func (s *S) TestRemoveUnitsInvalidValues(c *gocheck.C) {
	var tests = []struct {
		n        uint
		expected string
	}{
		{0, "Cannot remove zero units."},
		{4, "Cannot remove all units from an app."},
		{5, "Cannot remove 5 units from this app, it has only 4 units."},
	}
	app := App{
		Name:      "chemistry",
		Framework: "python",
		Units: []Unit{
			{Name: "chemistry/0"},
			{Name: "chemistry/1"},
			{Name: "chemistry/2"},
			{Name: "chemistry/3"},
		},
	}
	err := s.conn.Apps().Insert(app)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": app.Name})
	s.provisioner.Provision(&app)
	defer s.provisioner.Destroy(&app)
	s.provisioner.AddUnits(&app, 4)
	for _, test := range tests {
		err := app.RemoveUnits(test.n)
		c.Check(err, gocheck.NotNil)
		c.Check(err.Error(), gocheck.Equals, test.expected)
	}
}

func (s *S) TestRemoveUnitsFailureInProvisioner(c *gocheck.C) {
	s.provisioner.PrepareFailure("RemoveUnit", stderr.New("Cannot remove this unit."))
	app := App{
		Name:      "paradisum",
		Framework: "python",
		Units:     []Unit{{Name: "paradisum/0"}, {Name: "paradisum/1"}},
	}
	err := s.conn.Apps().Insert(app)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": app.Name})
	s.provisioner.Provision(&app)
	defer s.provisioner.Destroy(&app)
	err = app.RemoveUnits(1)
	c.Assert(err, gocheck.NotNil)
	c.Assert(err.Error(), gocheck.Equals, "Cannot remove this unit.")
}

func (s *S) TestRemoveUnitsFromIndicesSlice(c *gocheck.C) {
	var tests = []struct {
		input    []Unit
		indices  []int
		expected []Unit
	}{
		{
			input:    []Unit{{Name: "unit1"}, {Name: "unit2"}, {Name: "unit3"}, {Name: "unit4"}},
			indices:  []int{0, 1, 2},
			expected: []Unit{{Name: "unit4"}},
		},
		{
			input:    []Unit{{Name: "unit1"}, {Name: "unit2"}, {Name: "unit3"}, {Name: "unit4"}},
			indices:  []int{0, 3, 4},
			expected: []Unit{{Name: "unit2"}},
		},
		{
			input:    []Unit{{Name: "unit1"}, {Name: "unit2"}, {Name: "unit3"}, {Name: "unit4"}},
			indices:  []int{4},
			expected: []Unit{{Name: "unit1"}, {Name: "unit2"}, {Name: "unit3"}},
		},
	}
	for _, t := range tests {
		a := App{Units: t.input}
		a.removeUnits(t.indices)
		c.Check(a.Units, gocheck.DeepEquals, t.expected)
	}
}

func (s *S) TestRemoveUnitByNameOrInstanceId(c *gocheck.C) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := atomic.LoadInt32(&calls)
		atomic.StoreInt32(&calls, v+1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	srvc := service.Service{Name: "nosql", Endpoint: map[string]string{"production": ts.URL}}
	err := srvc.Create()
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Services().Remove(bson.M{"_id": "nosql"})
	app := App{
		Name:      "physics",
		Framework: "python",
		Units: []Unit{
			{Name: "physics/0"},
		},
	}
	instance := service.ServiceInstance{
		Name:        "my-mysql",
		ServiceName: "nosql",
		Teams:       []string{s.team.Name},
		Apps:        []string{app.Name},
	}
	instance.Create()
	defer s.conn.ServiceInstances().Remove(bson.M{"name": "my-mysql"})
	err = s.conn.Apps().Insert(app)
	c.Assert(err, gocheck.IsNil)
	err = s.provisioner.Provision(&app)
	c.Assert(err, gocheck.IsNil)
	err = app.AddUnits(4)
	c.Assert(err, gocheck.IsNil)
	defer func() {
		s.provisioner.Destroy(&app)
		s.conn.Apps().Remove(bson.M{"name": app.Name})
	}()
	otherApp := App{Name: app.Name, Units: app.Units}
	err = otherApp.RemoveUnit(app.Units[0].Name)
	c.Assert(err, gocheck.IsNil)
	err = app.Get()
	c.Assert(err, gocheck.IsNil)
	c.Assert(app.Framework, gocheck.Equals, "python")
	c.Assert(app.Units, gocheck.HasLen, 4)
	c.Assert(app.Units[0].Name, gocheck.Equals, "physics/1")
	c.Assert(app.Units[1].Name, gocheck.Equals, "physics/2")
	c.Assert(app.Units[2].Name, gocheck.Equals, "physics/3")
	c.Assert(app.Units[3].Name, gocheck.Equals, "physics/4")
	err = app.RemoveUnit(app.Units[1].InstanceId)
	c.Assert(err, gocheck.IsNil)
	units := s.provisioner.GetUnits(&app)
	c.Assert(units, gocheck.HasLen, 3)
	c.Assert(units[0].Name, gocheck.Equals, "physics/1")
	c.Assert(units[1].Name, gocheck.Equals, "physics/3")
	c.Assert(units[2].Name, gocheck.Equals, "physics/4")
	ok := make(chan int8)
	go func() {
		for _ = range time.Tick(1e3) {
			if atomic.LoadInt32(&calls) == 2 {
				ok <- 1
				return
			}
		}
	}()
	select {
	case <-ok:
	case <-time.After(2e9):
		c.Fatal("Did not call service endpoint twice.")
	}
}

func (s *S) TestRemoveAbsentUnit(c *gocheck.C) {
	app := App{
		Name:      "chemistry",
		Framework: "python",
		Units: []Unit{
			{Name: "chemistry/0"},
		},
	}
	err := s.conn.Apps().Insert(app)
	c.Assert(err, gocheck.IsNil)
	err = s.provisioner.Provision(&app)
	c.Assert(err, gocheck.IsNil)
	err = app.AddUnits(1)
	c.Assert(err, gocheck.IsNil)
	defer func() {
		s.provisioner.Destroy(&app)
		s.conn.Apps().Remove(bson.M{"name": app.Name})
	}()
	err = app.Get()
	c.Assert(err, gocheck.IsNil)
	instId := app.Units[1].InstanceId
	err = app.RemoveUnit(instId)
	c.Assert(err, gocheck.IsNil)
	err = app.RemoveUnit(instId)
	c.Assert(err, gocheck.NotNil)
	c.Assert(err, gocheck.ErrorMatches, "Unit not found.")
	err = app.Get()
	c.Assert(err, gocheck.IsNil)
	c.Assert(app.Units, gocheck.HasLen, 1)
	c.Assert(app.Units[0].Name, gocheck.Equals, "chemistry/0")
	units := s.provisioner.GetUnits(&app)
	c.Assert(units, gocheck.HasLen, 1)
	c.Assert(units[0].Name, gocheck.Equals, "chemistry/0")
}

func (s *S) TestGrantAccess(c *gocheck.C) {
	a := App{Name: "appName", Framework: "django", Teams: []string{}}
	err := a.Grant(&s.team)
	c.Assert(err, gocheck.IsNil)
	_, found := a.find(&s.team)
	c.Assert(found, gocheck.Equals, true)
}

func (s *S) TestGrantAccessKeepTeamsSorted(c *gocheck.C) {
	a := App{Name: "appName", Framework: "django", Teams: []string{"acid-rain", "zito"}}
	err := a.Grant(&s.team)
	c.Assert(err, gocheck.IsNil)
	c.Assert(a.Teams, gocheck.DeepEquals, []string{"acid-rain", s.team.Name, "zito"})
}

func (s *S) TestGrantAccessFailsIfTheTeamAlreadyHasAccessToTheApp(c *gocheck.C) {
	a := App{Name: "appName", Framework: "django", Teams: []string{s.team.Name}}
	err := a.Grant(&s.team)
	c.Assert(err, gocheck.NotNil)
	c.Assert(err, gocheck.ErrorMatches, "^This team already has access to this app$")
}

func (s *S) TestRevokeAccess(c *gocheck.C) {
	a := App{Name: "appName", Framework: "django", Teams: []string{s.team.Name}}
	err := a.Revoke(&s.team)
	c.Assert(err, gocheck.IsNil)
	_, found := a.find(&s.team)
	c.Assert(found, gocheck.Equals, false)
}

func (s *S) TestRevoke(c *gocheck.C) {
	a := App{Name: "test", Teams: []string{"team1", "team2", "team3", "team4"}}
	err := a.Revoke(&auth.Team{Name: "team2"})
	c.Assert(err, gocheck.IsNil)
	c.Assert(a.Teams, gocheck.DeepEquals, []string{"team1", "team3", "team4"})
	err = a.Revoke(&auth.Team{Name: "team4"})
	c.Assert(err, gocheck.IsNil)
	c.Assert(a.Teams, gocheck.DeepEquals, []string{"team1", "team3"})
	err = a.Revoke(&auth.Team{Name: "team1"})
	c.Assert(err, gocheck.IsNil)
	c.Assert(a.Teams, gocheck.DeepEquals, []string{"team3"})
}

func (s *S) TestRevokeAccessFailsIfTheTeamsDoesNotHaveAccessToTheApp(c *gocheck.C) {
	a := App{Name: "appName", Framework: "django", Teams: []string{}}
	err := a.Revoke(&s.team)
	c.Assert(err, gocheck.NotNil)
	c.Assert(err, gocheck.ErrorMatches, "^This team does not have access to this app$")
}

func (s *S) TestSetEnvNewAppsTheMapIfItIsNil(c *gocheck.C) {
	a := App{Name: "how-many-more-times"}
	c.Assert(a.Env, gocheck.IsNil)
	env := bind.EnvVar{Name: "PATH", Value: "/"}
	a.setEnv(env)
	c.Assert(a.Env, gocheck.NotNil)
}

func (s *S) TestSetEnvironmentVariableToApp(c *gocheck.C) {
	a := App{Name: "appName", Framework: "django"}
	a.setEnv(bind.EnvVar{Name: "PATH", Value: "/", Public: true})
	env := a.Env["PATH"]
	c.Assert(env.Name, gocheck.Equals, "PATH")
	c.Assert(env.Value, gocheck.Equals, "/")
	c.Assert(env.Public, gocheck.Equals, true)
}

func (s *S) TestSetEnvRespectsThePublicOnlyFlagKeepPrivateVariablesWhenItsTrue(c *gocheck.C) {
	a := App{
		Name:  "myapp",
		Units: []Unit{{Machine: 1}},
		Env: map[string]bind.EnvVar{
			"DATABASE_HOST": {
				Name:   "DATABASE_HOST",
				Value:  "localhost",
				Public: false,
			},
		},
	}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	envs := []bind.EnvVar{
		{
			Name:   "DATABASE_HOST",
			Value:  "remotehost",
			Public: false,
		},
		{
			Name:   "DATABASE_PASSWORD",
			Value:  "123",
			Public: true,
		},
	}
	err = a.setEnvsToApp(envs, true, false)
	c.Assert(err, gocheck.IsNil)
	newApp := App{Name: a.Name}
	err = newApp.Get()
	c.Assert(err, gocheck.IsNil)
	expected := map[string]bind.EnvVar{
		"DATABASE_HOST": {
			Name:   "DATABASE_HOST",
			Value:  "localhost",
			Public: false,
		},
		"DATABASE_PASSWORD": {
			Name:   "DATABASE_PASSWORD",
			Value:  "123",
			Public: true,
		},
	}
	c.Assert(newApp.Env, gocheck.DeepEquals, expected)
}

func (s *S) TestSetEnvRespectsThePublicOnlyFlagOverwrittenAllVariablesWhenItsFalse(c *gocheck.C) {
	a := App{
		Name: "myapp",
		Units: []Unit{
			{Machine: 1},
		},
		Env: map[string]bind.EnvVar{
			"DATABASE_HOST": {
				Name:   "DATABASE_HOST",
				Value:  "localhost",
				Public: false,
			},
		},
	}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	envs := []bind.EnvVar{
		{
			Name:   "DATABASE_HOST",
			Value:  "remotehost",
			Public: true,
		},
		{
			Name:   "DATABASE_PASSWORD",
			Value:  "123",
			Public: true,
		},
	}
	err = a.setEnvsToApp(envs, false, false)
	c.Assert(err, gocheck.IsNil)
	newApp := App{Name: a.Name}
	err = newApp.Get()
	c.Assert(err, gocheck.IsNil)
	expected := map[string]bind.EnvVar{
		"DATABASE_HOST": {
			Name:   "DATABASE_HOST",
			Value:  "remotehost",
			Public: true,
		},
		"DATABASE_PASSWORD": {
			Name:   "DATABASE_PASSWORD",
			Value:  "123",
			Public: true,
		},
	}
	c.Assert(newApp.Env, gocheck.DeepEquals, expected)
}

func (s *S) TestUnsetEnvRespectsThePublicOnlyFlagKeepPrivateVariablesWhenItsTrue(c *gocheck.C) {
	a := App{
		Name: "myapp",
		Units: []Unit{
			{Machine: 1},
		},
		Env: map[string]bind.EnvVar{
			"DATABASE_HOST": {
				Name:   "DATABASE_HOST",
				Value:  "localhost",
				Public: false,
			},
			"DATABASE_PASSWORD": {
				Name:   "DATABASE_PASSWORD",
				Value:  "123",
				Public: true,
			},
		},
	}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	err = a.UnsetEnvs([]string{"DATABASE_HOST", "DATABASE_PASSWORD"}, true)
	c.Assert(err, gocheck.IsNil)
	newApp := App{Name: a.Name}
	err = newApp.Get()
	c.Assert(err, gocheck.IsNil)
	expected := map[string]bind.EnvVar{
		"DATABASE_HOST": {
			Name:   "DATABASE_HOST",
			Value:  "localhost",
			Public: false,
		},
	}
	c.Assert(newApp.Env, gocheck.DeepEquals, expected)
}

func (s *S) TestUnsetEnvRespectsThePublicOnlyFlagUnsettingAllVariablesWhenItsFalse(c *gocheck.C) {
	a := App{
		Name: "myapp",
		Units: []Unit{
			{Machine: 1},
		},
		Env: map[string]bind.EnvVar{
			"DATABASE_HOST": {
				Name:   "DATABASE_HOST",
				Value:  "localhost",
				Public: false,
			},
			"DATABASE_PASSWORD": {
				Name:   "DATABASE_PASSWORD",
				Value:  "123",
				Public: true,
			},
		},
	}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	err = a.UnsetEnvs([]string{"DATABASE_HOST", "DATABASE_PASSWORD"}, false)
	c.Assert(err, gocheck.IsNil)
	newApp := App{Name: a.Name}
	err = newApp.Get()
	c.Assert(err, gocheck.IsNil)
	c.Assert(newApp.Env, gocheck.DeepEquals, map[string]bind.EnvVar{})
}

func (s *S) TestGetEnvironmentVariableFromApp(c *gocheck.C) {
	a := App{Name: "whole-lotta-love"}
	a.setEnv(bind.EnvVar{Name: "PATH", Value: "/"})
	v, err := a.getEnv("PATH")
	c.Assert(err, gocheck.IsNil)
	c.Assert(v.Value, gocheck.Equals, "/")
}

func (s *S) TestGetEnvReturnsErrorIfTheVariableIsNotDeclared(c *gocheck.C) {
	a := App{Name: "what-is-and-what-should-never"}
	a.Env = make(map[string]bind.EnvVar)
	_, err := a.getEnv("PATH")
	c.Assert(err, gocheck.NotNil)
}

func (s *S) TestGetEnvReturnsErrorIfTheEnvironmentMapIsNil(c *gocheck.C) {
	a := App{Name: "what-is-and-what-should-never"}
	_, err := a.getEnv("PATH")
	c.Assert(err, gocheck.NotNil)
}

func (s *S) TestInstanceEnvironmentReturnEnvironmentVariablesForTheServer(c *gocheck.C) {
	envs := map[string]bind.EnvVar{
		"DATABASE_HOST": {Name: "DATABASE_HOST", Value: "localhost", Public: false, InstanceName: "mysql"},
		"DATABASE_USER": {Name: "DATABASE_USER", Value: "root", Public: true, InstanceName: "mysql"},
		"HOST":          {Name: "HOST", Value: "10.0.2.1", Public: false, InstanceName: "redis"},
	}
	expected := map[string]bind.EnvVar{
		"DATABASE_HOST": {Name: "DATABASE_HOST", Value: "localhost", Public: false, InstanceName: "mysql"},
		"DATABASE_USER": {Name: "DATABASE_USER", Value: "root", Public: true, InstanceName: "mysql"},
	}
	a := App{Name: "hi-there", Env: envs}
	c.Assert(a.InstanceEnv("mysql"), gocheck.DeepEquals, expected)
}

func (s *S) TestInstanceEnvironmentDoesNotPanicIfTheEnvMapIsNil(c *gocheck.C) {
	a := App{Name: "hi-there"}
	c.Assert(a.InstanceEnv("mysql"), gocheck.DeepEquals, map[string]bind.EnvVar{})
}

func (s *S) TestSetCName(c *gocheck.C) {
	a := App{Name: "ktulu"}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	err = a.SetCName("ktulu.mycompany.com")
	c.Assert(err, gocheck.IsNil)
	err = a.Get()
	c.Assert(err, gocheck.IsNil)
	c.Assert(a.CName, gocheck.Equals, "ktulu.mycompany.com")
}

func (s *S) TestSetCNamePartialUpdate(c *gocheck.C) {
	a := App{Name: "master", Framework: "puppet"}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	other := App{Name: a.Name}
	err = other.SetCName("ktulu.mycompany.com")
	c.Assert(err, gocheck.IsNil)
	err = other.Get()
	c.Assert(other.Framework, gocheck.Equals, "puppet")
	c.Assert(other.Name, gocheck.Equals, "master")
	c.Assert(other.CName, gocheck.Equals, "ktulu.mycompany.com")
}

func (s *S) TestSetCNameUnknownApp(c *gocheck.C) {
	a := App{Name: "ktulu"}
	err := a.SetCName("ktulu.mycompany.com")
	c.Assert(err, gocheck.NotNil)
}

func (s *S) TestSetCNameValidatesTheCName(c *gocheck.C) {
	var data = []struct {
		input string
		valid bool
	}{
		{"ktulu.mycompany.com", true},
		{"ktulu-super.mycompany.com", true},
		{"ktulu_super.mycompany.com", true},
		{"KTULU.MYCOMPANY.COM", true},
		{"ktulu", true},
		{"KTULU", true},
		{"http://ktulu.mycompany.com", false},
		{"http:ktulu.mycompany.com", false},
		{"/ktulu.mycompany.com", false},
		{".ktulu.mycompany.com", false},
		{"0800.com", true},
		{"-0800.com", false},
		{"", true},
	}
	a := App{Name: "live-to-die"}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	for _, t := range data {
		err := a.SetCName(t.input)
		if !t.valid {
			c.Check(err.Error(), gocheck.Equals, "Invalid cname")
		} else {
			c.Check(err, gocheck.IsNil)
		}
	}
}

func (s *S) TestIsValid(c *gocheck.C) {
	var data = []struct {
		name     string
		expected bool
	}{
		{"myappmyappmyappmyappmyappmyappmyappmyappmyappmyappmyappmyappmyapp", false},
		{"myappmyappmyappmyappmyappmyappmyappmyappmyappmyappmyappmyappmyap", false},
		{"myappmyappmyappmyappmyappmyappmyappmyappmyappmyappmyappmyappmya", true},
		{"myApp", false},
		{"my app", false},
		{"123myapp", false},
		{"myapp", true},
		{"_theirapp", false},
		{"my-app", true},
		{"-myapp", false},
		{"my_app", false},
		{"b", true},
	}
	for _, d := range data {
		a := App{Name: d.name}
		if valid := a.isValid(); valid != d.expected {
			c.Errorf("Is %q a valid app name? Expected: %v. Got: %v.", d.name, d.expected, valid)
		}
	}
}

func (s *S) TestDeployHookAbsPath(c *gocheck.C) {
	pwd, err := os.Getwd()
	c.Assert(err, gocheck.IsNil)
	old, err := config.Get("git:unit-repo")
	c.Assert(err, gocheck.IsNil)
	config.Set("git:unit-repo", pwd)
	defer config.Set("git:unit-repo", old)
	expected := path.Join(pwd, "testdata", "pre.sh")
	command := "testdata/pre.sh"
	got, err := deployHookAbsPath(command)
	c.Assert(err, gocheck.IsNil)
	c.Assert(got, gocheck.Equals, expected)
}

func (s *S) TestDeployHookAbsPathAbsoluteCommands(c *gocheck.C) {
	command := "python manage.py syncdb --noinput"
	expected := "python manage.py syncdb --noinput"
	got, err := deployHookAbsPath(command)
	c.Assert(err, gocheck.IsNil)
	c.Assert(got, gocheck.Equals, expected)
}

func (s *S) TestLoadHooks(c *gocheck.C) {
	output := `pre-restart:
  - testdata/pre.sh
post-restart:
  - testdata/pos.sh
`
	s.provisioner.PrepareOutput([]byte(output))
	a := App{
		Name:      "something",
		Framework: "django",
		Units:     []Unit{{Name: "i-0800", State: "started"}},
	}
	err := a.loadHooks()
	c.Assert(err, gocheck.IsNil)
	c.Assert(a.hooks.PreRestart, gocheck.DeepEquals, []string{"testdata/pre.sh"})
	c.Assert(a.hooks.PosRestart, gocheck.DeepEquals, []string{"testdata/pos.sh"})
}

func (s *S) TestLoadHooksWithListOfCommands(c *gocheck.C) {
	output := `pre-restart:
  - testdata/pre.sh
  - ls -lh
  - sudo rm -rf /
post-restart:
  - testdata/pos.sh
`
	s.provisioner.PrepareOutput([]byte(output))
	a := App{
		Name:      "something",
		Framework: "django",
		Units:     []Unit{{Name: "i-0800", State: "started"}},
	}
	err := a.loadHooks()
	c.Assert(err, gocheck.IsNil)
	c.Assert(a.hooks.PreRestart, gocheck.DeepEquals, []string{"testdata/pre.sh", "ls -lh", "sudo rm -rf /"})
	c.Assert(a.hooks.PosRestart, gocheck.DeepEquals, []string{"testdata/pos.sh"})
}

func (s *S) TestLoadHooksWithError(c *gocheck.C) {
	a := App{Name: "something", Framework: "django"}
	err := a.loadHooks()
	c.Assert(err, gocheck.IsNil)
	c.Assert(a.hooks.PreRestart, gocheck.IsNil)
	c.Assert(a.hooks.PosRestart, gocheck.IsNil)
}

func (s *S) TestPreRestart(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("pre-restarted"))
	a := App{
		Name:      "something",
		Framework: "django",
		hooks: &conf{
			PreRestart: []string{"pre.sh"},
			PosRestart: []string{"pos.sh"},
		},
		Units: []Unit{{Name: "i-0800", State: "started"}},
	}
	w := new(bytes.Buffer)
	err := a.preRestart(w)
	c.Assert(err, gocheck.IsNil)
	c.Assert(err, gocheck.IsNil)
	st := strings.Replace(w.String(), "\n", "###", -1)
	c.Assert(st, gocheck.Matches, `.*### ---> Running pre-restart###.*pre-restarted$`)
	cmds := s.provisioner.GetCmds("", &a)
	c.Assert(cmds, gocheck.HasLen, 1)
	c.Assert(cmds[0].Cmd, gocheck.Matches, `^\[ -f /home/application/apprc \] && source /home/application/apprc; \[ -d /home/application/current \] && cd /home/application/current;.*pre.sh$`)
}

func (s *S) TestPreRestartWhenAppConfDoesNotExist(c *gocheck.C) {
	a := App{Name: "something", Framework: "django"}
	w := new(bytes.Buffer)
	l := stdlog.New(w, "", stdlog.LstdFlags)
	log.SetLogger(l)
	err := a.preRestart(w)
	c.Assert(err, gocheck.IsNil)
	st := strings.Split(w.String(), "\n")
	regexp := ".*Skipping pre-restart hooks..."
	c.Assert(st[0], gocheck.Matches, regexp)
}

func (s *S) TestSkipsPreRestartWhenPreRestartSectionDoesNotExists(c *gocheck.C) {
	a := App{
		Name:      "something",
		Framework: "django",
		Units:     []Unit{{State: string(provision.StatusStarted), Machine: 1}},
		hooks:     &conf{PosRestart: []string{"somescript.sh"}},
	}
	w := new(bytes.Buffer)
	l := stdlog.New(w, "", stdlog.LstdFlags)
	log.SetLogger(l)
	err := a.preRestart(w)
	c.Assert(err, gocheck.IsNil)
	st := strings.Split(w.String(), "\n")
	c.Assert(st[0], gocheck.Matches, ".*Skipping pre-restart hooks...")
}

func (s *S) TestPosRestart(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("restarted"))
	a := App{
		Name:      "something",
		Framework: "django",
		hooks:     &conf{PosRestart: []string{"pos.sh"}},
		Units:     []Unit{{Name: "i-0800", State: "started"}},
	}
	w := new(bytes.Buffer)
	err := a.postRestart(w)
	c.Assert(err, gocheck.IsNil)
	st := strings.Replace(w.String(), "\n", "###", -1)
	c.Assert(st, gocheck.Matches, `.*restarted$`)
}

func (s *S) TestPosRestartWhenAppConfDoesNotExists(c *gocheck.C) {
	a := App{Name: "something", Framework: "django"}
	w := new(bytes.Buffer)
	l := stdlog.New(w, "", stdlog.LstdFlags)
	log.SetLogger(l)
	err := a.postRestart(w)
	c.Assert(err, gocheck.IsNil)
	st := strings.Split(w.String(), "\n")
	c.Assert(st[0], gocheck.Matches, ".*Skipping post-restart hooks...")
}

func (s *S) TestSkipsPosRestartWhenPosRestartSectionDoesNotExists(c *gocheck.C) {
	a := App{
		Name:      "something",
		Framework: "django",
		Units:     []Unit{{State: string(provision.StatusStarted), Machine: 1}},
		hooks:     &conf{PreRestart: []string{"somescript.sh"}},
	}
	w := new(bytes.Buffer)
	l := stdlog.New(w, "", stdlog.LstdFlags)
	log.SetLogger(l)
	err := a.postRestart(w)
	c.Assert(err, gocheck.IsNil)
	st := strings.Split(w.String(), "\n")
	c.Assert(st[0], gocheck.Matches, ".*Skipping post-restart hooks...")
}

func (s *S) TestInstallDeps(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("dependencies installed"))
	a := App{
		Name:      "someApp",
		Framework: "django",
		Teams:     []string{s.team.Name},
		Units:     []Unit{{Name: "i-0800", State: "started"}},
	}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	var buf bytes.Buffer
	err = a.InstallDeps(&buf)
	c.Assert(err, gocheck.IsNil)
	c.Assert(buf.String(), gocheck.Equals, "dependencies installed")
	cmds := s.provisioner.GetCmds("/var/lib/tsuru/hooks/dependencies", &a)
	c.Assert(cmds, gocheck.HasLen, 1)
}

func (s *S) TestRestart(c *gocheck.C) {
	s.provisioner.PrepareOutput(nil) // loadHooks
	a := App{
		Name:      "someApp",
		Framework: "django",
		Teams:     []string{s.team.Name},
		Units:     []Unit{{Name: "i-0800", State: "started"}},
	}
	var b bytes.Buffer
	err := a.Restart(&b)
	c.Assert(err, gocheck.IsNil)
	result := strings.Replace(b.String(), "\n", "#", -1)
	c.Assert(result, gocheck.Matches, ".*# ---> Restarting your app#.*")
	restarts := s.provisioner.Restarts(&a)
	c.Assert(restarts, gocheck.Equals, 1)
}

func (s *S) TestRestartRunsPreRestartHook(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("pre-restart-by-restart"))
	a := App{
		Name:      "someApp",
		Framework: "django",
		Teams:     []string{s.team.Name},
		hooks:     &conf{PreRestart: []string{"pre.sh"}},
		Units:     []Unit{{Name: "i-0800", State: "started"}},
	}
	var buf bytes.Buffer
	err := a.Restart(&buf)
	c.Assert(err, gocheck.IsNil)
	content := buf.String()
	content = strings.Replace(content, "\n", "###", -1)
	c.Assert(content, gocheck.Matches, "^.*### ---> Running pre-restart###.*$")
}

func (s *S) TestRestartRunsPosRestartHook(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("post-restart-by-restart"))
	a := App{
		Name:      "someApp",
		Framework: "django",
		Teams:     []string{s.team.Name},
		hooks:     &conf{PosRestart: []string{"pos.sh"}},
		Units:     []Unit{{Name: "i-0800", State: "started"}},
	}
	var buf bytes.Buffer
	err := a.Restart(&buf)
	c.Assert(err, gocheck.IsNil)
	content := buf.String()
	content = strings.Replace(content, "\n", "###", -1)
	c.Assert(content, gocheck.Matches, "^.*### ---> Running post-restart###.*$")
}

func (s *S) TestLog(c *gocheck.C) {
	a := App{Name: "newApp"}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer func() {
		s.conn.Apps().Remove(bson.M{"name": a.Name})
		s.conn.Logs().Remove(bson.M{"appname": a.Name})
	}()
	err = a.Log("last log msg", "tsuru")
	c.Assert(err, gocheck.IsNil)
	var logs []Applog
	err = s.conn.Logs().Find(bson.M{"appname": a.Name}).All(&logs)
	c.Assert(err, gocheck.IsNil)
	c.Assert(logs, gocheck.HasLen, 1)
	c.Assert(logs[0].Message, gocheck.Equals, "last log msg")
	c.Assert(logs[0].Source, gocheck.Equals, "tsuru")
	c.Assert(logs[0].AppName, gocheck.Equals, a.Name)
}

func (s *S) TestLogShouldAddOneRecordByLine(c *gocheck.C) {
	a := App{Name: "newApp"}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer func() {
		s.conn.Apps().Remove(bson.M{"name": a.Name})
		s.conn.Logs().Remove(bson.M{"appname": a.Name})
	}()
	err = a.Log("last log msg\nfirst log", "source")
	c.Assert(err, gocheck.IsNil)
	var logs []Applog
	err = s.conn.Logs().Find(bson.M{"appname": a.Name}).All(&logs)
	c.Assert(err, gocheck.IsNil)
	c.Assert(logs, gocheck.HasLen, 2)
	c.Assert(logs[0].Message, gocheck.Equals, "last log msg")
	c.Assert(logs[1].Message, gocheck.Equals, "first log")
}

func (s *S) TestLogShouldNotLogBlankLines(c *gocheck.C) {
	a := App{Name: "ich"}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	err = a.Log("some message", "tsuru")
	c.Assert(err, gocheck.IsNil)
	err = a.Log("", "")
	c.Assert(err, gocheck.IsNil)
	count, err := s.conn.Logs().Find(bson.M{"appname": a.Name}).Count()
	c.Assert(err, gocheck.IsNil)
	c.Assert(count, gocheck.Equals, 1)
}

func (s *S) TestLogWithListeners(c *gocheck.C) {
	var logs struct {
		l []Applog
		sync.Mutex
	}
	a := App{
		Name: "newApp",
	}
	err := s.conn.Apps().Insert(a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	l := NewLogListener(&a)
	defer l.Close()
	go func() {
		for log := range l.C {
			logs.Lock()
			logs.l = append(logs.l, log)
			logs.Unlock()
		}
	}()
	err = a.Log("last log msg", "tsuru")
	c.Assert(err, gocheck.IsNil)
	done := make(chan bool, 1)
	q := make(chan bool)
	go func(quit chan bool) {
		for _ = range time.Tick(1e3) {
			select {
			case <-quit:
				return
			default:
			}
			logs.Lock()
			if len(logs.l) == 1 {
				logs.Unlock()
				done <- true
				return
			}
			logs.Unlock()
		}
	}(q)
	select {
	case <-done:
	case <-time.After(2e9):
		defer close(q)
		c.Fatal("Timed out.")
	}
	logs.Lock()
	c.Assert(logs.l, gocheck.HasLen, 1)
	log := logs.l[0]
	logs.Unlock()
	c.Assert(log.Message, gocheck.Equals, "last log msg")
	c.Assert(log.Source, gocheck.Equals, "tsuru")
}

func (s *S) TestLastLogs(c *gocheck.C) {
	app := App{
		Name:      "app3",
		Framework: "vougan",
		Teams:     []string{s.team.Name},
	}
	err := s.conn.Apps().Insert(app)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": app.Name})
	for i := 0; i < 15; i++ {
		app.Log(strconv.Itoa(i), "tsuru")
		time.Sleep(1e6) // let the time flow
	}
	app.Log("app3 log from circus", "circus")
	logs, err := app.LastLogs(10, "tsuru")
	c.Assert(err, gocheck.IsNil)
	c.Assert(logs, gocheck.HasLen, 10)
	for i := 5; i < 15; i++ {
		c.Check(logs[i-5].Message, gocheck.Equals, strconv.Itoa(i))
		c.Check(logs[i-5].Source, gocheck.Equals, "tsuru")
	}
}

func (s *S) TestGetTeams(c *gocheck.C) {
	app := App{Name: "app", Teams: []string{s.team.Name}}
	teams := app.GetTeams()
	c.Assert(teams, gocheck.HasLen, 1)
	c.Assert(teams[0].Name, gocheck.Equals, s.team.Name)
}

func (s *S) TestSetTeams(c *gocheck.C) {
	app := App{Name: "app"}
	app.SetTeams([]auth.Team{s.team})
	c.Assert(app.Teams, gocheck.DeepEquals, []string{s.team.Name})
}

func (s *S) TestSetTeamsSortTeamNames(c *gocheck.C) {
	app := App{Name: "app"}
	app.SetTeams([]auth.Team{s.team, {Name: "zzz"}, {Name: "aaa"}})
	c.Assert(app.Teams, gocheck.DeepEquals, []string{"aaa", s.team.Name, "zzz"})
}

func (s *S) TestGetUnits(c *gocheck.C) {
	app := App{Units: []Unit{{Ip: "1.1.1.1"}}}
	expected := []bind.Unit{bind.Unit(&Unit{Ip: "1.1.1.1", app: &app})}
	c.Assert(app.GetUnits(), gocheck.DeepEquals, expected)
}

func (s *S) TestAppMarshalJson(c *gocheck.C) {
	app := App{
		Name:      "name",
		Framework: "Framework",
		Teams:     []string{"team1"},
		Ip:        "10.10.10.1",
		CName:     "name.mycompany.com",
	}
	expected := make(map[string]interface{})
	expected["Name"] = "name"
	expected["Framework"] = "Framework"
	expected["Repository"] = repository.GetUrl(app.Name)
	expected["Teams"] = []interface{}{"team1"}
	expected["Units"] = nil
	expected["Ip"] = "10.10.10.1"
	expected["CName"] = "name.mycompany.com"
	data, err := app.MarshalJSON()
	c.Assert(err, gocheck.IsNil)
	result := make(map[string]interface{})
	err = json.Unmarshal(data, &result)
	c.Assert(err, gocheck.IsNil)
	c.Assert(result, gocheck.DeepEquals, expected)
}

func (s *S) TestRun(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("a lot of files"))
	app := App{
		Name:  "myapp",
		Units: []Unit{{Name: "i-0800", State: "started"}},
	}
	var buf bytes.Buffer
	err := app.Run("ls -lh", &buf)
	c.Assert(err, gocheck.IsNil)
	c.Assert(buf.String(), gocheck.Equals, "a lot of files")
	expected := "[ -f /home/application/apprc ] && source /home/application/apprc;"
	expected += " [ -d /home/application/current ] && cd /home/application/current;"
	expected += " ls -lh"
	cmds := s.provisioner.GetCmds(expected, &app)
	c.Assert(cmds, gocheck.HasLen, 1)
}

func (s *S) TestRunWithoutEnv(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("a lot of files"))
	app := App{
		Name:  "myapp",
		Units: []Unit{{Name: "i-0800", State: "started"}},
	}
	var buf bytes.Buffer
	err := app.run("ls -lh", &buf)
	c.Assert(err, gocheck.IsNil)
	c.Assert(buf.String(), gocheck.Equals, "a lot of files")
	cmds := s.provisioner.GetCmds("ls -lh", &app)
	c.Assert(cmds, gocheck.HasLen, 1)
}

func (s *S) TestCommand(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("lots of files"))
	app := App{Name: "myapp"}
	var buf bytes.Buffer
	err := app.Command(&buf, &buf, "ls -lh")
	c.Assert(err, gocheck.IsNil)
	c.Assert(buf.String(), gocheck.Equals, "lots of files")
	cmds := s.provisioner.GetCmds("ls -lh", &app)
	c.Assert(cmds, gocheck.HasLen, 1)
}

func (s *S) TestAppShouldBeARepositoryUnit(c *gocheck.C) {
	var _ repository.Unit = &App{}
}

func (s *S) TestSerializeEnvVars(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("exported"))
	app := App{
		Name:  "time",
		Teams: []string{s.team.Name},
		Env: map[string]bind.EnvVar{
			"http_proxy": {
				Name:   "http_proxy",
				Value:  "http://theirproxy.com:3128/",
				Public: true,
			},
		},
		Units: []Unit{{Name: "i-0800", State: "started"}},
	}
	err := app.serializeEnvVars()
	c.Assert(err, gocheck.IsNil)
	cmds := s.provisioner.GetCmds("", &app)
	c.Assert(cmds, gocheck.HasLen, 1)
	cmdRegexp := `^cat > /home/application/apprc <<END # generated by tsuru .*`
	cmdRegexp += ` export http_proxy="http://theirproxy.com:3128/" END $`
	cmd := strings.Replace(cmds[0].Cmd, "\n", " ", -1)
	c.Assert(cmd, gocheck.Matches, cmdRegexp)
}

func (s *S) TestSerializeEnvVarsErrorWithoutOutput(c *gocheck.C) {
	app := App{
		Name: "intheend",
		Env: map[string]bind.EnvVar{
			"https_proxy": {
				Name:   "https_proxy",
				Value:  "https://secureproxy.com:3128/",
				Public: true,
			},
		},
	}
	err := app.serializeEnvVars()
	c.Assert(err, gocheck.NotNil)
	c.Assert(err.Error(), gocheck.Equals, "Failed to write env vars: App must be available to run commands.")
}

func (s *S) TestSerializeEnvVarsErrorWithOutput(c *gocheck.C) {
	s.provisioner.PrepareOutput([]byte("This program has performed an illegal operation"))
	s.provisioner.PrepareFailure("ExecuteCommand", stderr.New("exit status 1"))
	app := App{
		Name: "intheend",
		Env: map[string]bind.EnvVar{
			"https_proxy": {
				Name:   "https_proxy",
				Value:  "https://secureproxy.com:3128/",
				Public: true,
			},
		},
		Units: []Unit{{Name: "i-0800", State: "started"}},
	}
	err := app.serializeEnvVars()
	c.Assert(err, gocheck.NotNil)
	expected := "Failed to write env vars (exit status 1): This program has performed an illegal operation."
	c.Assert(err.Error(), gocheck.Equals, expected)
}

func (s *S) TestListReturnsAppsForAGivenUser(c *gocheck.C) {
	a := App{
		Name:  "testapp",
		Teams: []string{s.team.Name},
	}
	a2 := App{
		Name:  "othertestapp",
		Teams: []string{"commonteam", s.team.Name},
	}
	err := s.conn.Apps().Insert(&a)
	c.Assert(err, gocheck.IsNil)
	err = s.conn.Apps().Insert(&a2)
	c.Assert(err, gocheck.IsNil)
	defer func() {
		s.conn.Apps().Remove(bson.M{"name": a.Name})
		s.conn.Apps().Remove(bson.M{"name": a2.Name})
	}()
	apps, err := List(s.user)
	c.Assert(err, gocheck.IsNil)
	c.Assert(len(apps), gocheck.Equals, 2)
}

func (s *S) TestListReturnsEmptyAppArrayWhenUserHasNoAccessToAnyApp(c *gocheck.C) {
	apps, err := List(s.user)
	c.Assert(err, gocheck.IsNil)
	c.Assert(apps, gocheck.DeepEquals, []App(nil))
}

func (s *S) TestListReturnsAllAppsWhenUserIsInAdminTeam(c *gocheck.C) {
	a := App{Name: "testApp", Teams: []string{"notAdmin", "noSuperUser"}}
	err := s.conn.Apps().Insert(&a)
	c.Assert(err, gocheck.IsNil)
	defer s.conn.Apps().Remove(bson.M{"name": a.Name})
	s.createAdminUserAndTeam(c)
	defer s.removeAdminUserAndTeam(c)
	apps, err := List(s.admin)
	c.Assert(len(apps), Greater, 0)
	c.Assert(apps[0].Name, gocheck.Equals, "testApp")
	c.Assert(apps[0].Teams, gocheck.DeepEquals, []string{"notAdmin", "noSuperUser"})
}

func (s *S) TestGetName(c *gocheck.C) {
	a := App{Name: "something"}
	c.Assert(a.GetName(), gocheck.Equals, a.Name)
}

func (s *S) TestGetIp(c *gocheck.C) {
	a := App{Ip: "10.10.10.10"}
	c.Assert(a.GetIp(), gocheck.Equals, a.Ip)
}

func (s *S) TestGetFramework(c *gocheck.C) {
	a := App{Framework: "django"}
	c.Assert(a.GetFramework(), gocheck.Equals, a.Framework)
}

func (s *S) TestGetProvisionUnits(c *gocheck.C) {
	a := App{Name: "anycolor", Units: []Unit{{Name: "i-0800"}, {Name: "i-0900"}, {Name: "i-a00"}}}
	gotUnits := a.ProvisionUnits()
	for i := range a.Units {
		if gotUnits[i].GetName() != a.Units[i].Name {
			c.Errorf("Failed at position %d: Want %q. Got %q.", i, a.Units[i].Name, gotUnits[i].GetName())
		}
	}
}

func (s *S) TestAppAvailableShouldReturnsTrueWhenOneUnitIsStarted(c *gocheck.C) {
	a := App{
		Name: "anycolor",
		Units: []Unit{
			{Name: "i-0800", State: "started"},
			{Name: "i-0900", State: "pending"},
			{Name: "i-a00", State: "stopped"},
		},
	}
	c.Assert(a.Available(), gocheck.Equals, true)
}
