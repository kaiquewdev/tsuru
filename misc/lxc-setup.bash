#!/bin/bash

# Copyright 2013 tsuru authors. All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

# This script is used to install tsuru with lxc provisioner.


echo "updating and upgrading"
sudo apt-get update
sudo apt-get upgrade -y

echo "installing lxc"
sudo apt-get install lxc -y

echo "installing beanstalkd"
sudo apt-get install -y beanstalkd

echo "install nginx"
sudo apt-get install nginx -y

echo "installing mongodb"
sudo apt-key adv --keyserver keyserver.ubuntu.com --recv 7F0CEB10
sudo bash -c 'echo "deb http://downloads-distro.mongodb.org/repo/ubuntu-upstart dist 10gen" > /etc/apt/sources.list.d/10gen.list'
sudo apt-get update
sudo apt-get install mongodb-10gen -y

echo "installing git"
sudo apt-get install git -y

echo "installing gandalf-wrapper"
curl -sL https://s3.amazonaws.com/tsuru/dist-server/gandalf-bin.tar.gz | sudo tar -xz -C /usr/bin

echo "installing gandalf-webserver"
curl -sL https://s3.amazonaws.com/tsuru/dist-server/gandalf-webserver.tar.gz | sudo tar -xz -C /usr/bin

echo "installing tsuru-api"
curl -sL https://s3.amazonaws.com/tsuru/dist-server/tsuru-api.tar.gz | sudo tar -xz -C /usr/bin

echo "installing tsuru-collector"
curl -sL https://s3.amazonaws.com/tsuru/dist-server/tsuru-collector.tar.gz | sudo tar -xz -C /usr/bin

echo "configuring tsuru"
sudo mkdir /etc/tsuru
sudo curl -sL https://raw.github.com/globocom/tsuru/master/etc/tsuru-lxc.conf -o /etc/tsuru/tsuru.conf

echo "configuring gandalf"
sudo bash -c 'echo "bin-path: /home/ubuntu/gandalf-bin
database:
  url: 127.0.0.1:27017
  name: gandalf
git:
  bare:
    location: /var/repositories
    template: /home/git/bare-template
  daemon:
    export-all: true
host: localhost
webserver:
  port: \":8000\"" > /etc/gandalf.conf'

echo "creating the git user"
sudo useradd git

echo "creating bare path"
sudo mkdir -p /var/repositories
sudo chown -R git:git /var/repositories

echo "creating template path"
sudo mkdir -l /home/gi$://raw.github.com/globocom/tsuru/master/misc/git-hooks/pre-receivei.py > /home/git/bare-template/hooks/pre-receive.pyt/bare-template/hooks
curl https://raw.github.com/globocom/tsuru/master/misc/git-hooks/post-receive > /home/git/bare-template/hooks/post-receive
curl https://raw.github.com/globocom/tsuru/master/misc/git-hooks/pre-receive > /home/git/bare-template/hooks/pre-receive
curl https://raw.github.com/globocom/tsuru/master/misc/git-hooks/pre-receivei.py > /home/git/bare-template/hooks/pre-receive.py
sudo chown -R git:git /home/git/bare-template

echo "generating the ssh-key for root"
sudo ssh-keygen -N "" -f /root/.ssh/id_rsa

echo "downloading charms"
git clone git://github.com/globocom/charms.git -b lxc

echo "starting mongodb"
sudo service mongodb start

echo "starting beanstalkd"
sudo service beanstalkd start

echo "starting gandalf webserver"
sudo -u git gandalf-webserver &

echo "starting git daemon"
sudo -u git git daemon --base-path=/var/repositories --syslog --export-all &

echo "starting tsuru-collector"
collector &

echo "starting tsuru-api"
sudo api &
