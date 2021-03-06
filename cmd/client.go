// Copyright 2013 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cmd

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
)

type Doer interface {
	Do(request *http.Request) (*http.Response, error)
}

type Client struct {
	HttpClient     *http.Client
	context        *Context
	progname       string
	currentVersion string
	versionHeader  string
}

func NewClient(client *http.Client, context *Context, manager *Manager) *Client {
	return &Client{
		HttpClient:     client,
		context:        context,
		progname:       manager.name,
		currentVersion: manager.version,
		versionHeader:  manager.versionHeader,
	}
}

func (c *Client) detectClientError(err error) error {
	urlErr, ok := err.(*url.Error)
	if !ok {
		return err
	}
	switch urlErr.Err.(type) {
	case x509.UnknownAuthorityError:
		target, _ := readTarget()
		return fmt.Errorf("Failed to connect to tsuru server (%s): %s", target, urlErr.Err)
	}
	target, _ := readTarget()
	return fmt.Errorf("Failed to connect to tsuru server (%s), it's probably down.", target)
}

func (c *Client) Do(request *http.Request) (*http.Response, error) {
	if token, err := readToken(); err == nil {
		request.Header.Set("Authorization", token)
	}
	response, err := c.HttpClient.Do(request)
	err = c.detectClientError(err)
	if err != nil {
		return nil, err
	}
	supported := response.Header.Get(c.versionHeader)
	format := `############################################################

WARNING: You're using an unsupported version of %s.

You must have at least version %s, your current
version is %s.

Please go to http://tsuru.rtfd.org/client-install and
download the last version.

############################################################

`
	if !validateVersion(supported, c.currentVersion) {
		fmt.Fprintf(c.context.Stderr, format, c.progname, supported, c.currentVersion)
	}
	if response.StatusCode > 399 {
		defer response.Body.Close()
		result, _ := ioutil.ReadAll(response.Body)
		return nil, errors.New(string(result))
	}
	return response, nil
}
