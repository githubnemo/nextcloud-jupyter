#!/bin/sh

set -e

user=$1
env_dir=$2
base_url=$3
token=$4
port=$5

if [ $# -lt 5 ]; then
	echo "Usage: $0 <user> <env_dir> <base_url> <token> <port>" >/dev/stderr
	exit 1
fi

user_dir="${env_dir}/${user}/"

sed -e "s#{token}#${token}#" \
	-e "s#{base_url}#${base_url}#" \
	-e "s#{port}#${port}#" \
	./jupyter_notebook_config.py.tmpl > "${user_dir}/jupyter_notebook_config.py"

cd "$user_dir"
. "$user_dir/env/bin/activate"

final() {
	echo "Final countdown... ($user)"
	echo "Final countdown... ($user)" >> /tmp/foolog
	jupyter notebook stop $port
}

trap final INT TERM EXIT

jupyter notebook --config="./jupyter_notebook_config.py"
