#!/bin/sh

set -e

user=$1
env_dir=$2
port=$3

if [ $# -lt 3 ]; then
	echo "Usage: $0 <user> <env_dir> <port>" >/dev/stderr
	exit 1
fi

user_dir="${env_dir}/${user}/"

cd "$user_dir"
. "$user_dir/env/bin/activate"

jupyter notebook stop -y $port
