#!/bin/sh

# expect
# - user=$1
# - env_dir=$2

set -e

user=$1
env_dir=$2

if [ $# -lt 2 ]; then
	echo "Usage: $0 <user> <env_dir>" >/dev/stderr
	exit 1
fi

user_dir="${env_dir}/${user}/"

if ! echo "$user_dir" | grep -E '^/'; then
	echo "user dir must be absolute ($user_dir)" >/dev/stderr
	exit 1
fi

if [ -d "$user_dir" ]; then
	echo "Already setup (it seems), skipping." >/dev/stderr
	exit 0
fi

mkdir "$env_dir" || true
mkdir "$user_dir" || true
mkdir "$user_dir/home" || true

cd "$user_dir"
virtualenv -p python3 "$user_dir/env"
. "$user_dir/env/bin/activate"
pip install jupyter
