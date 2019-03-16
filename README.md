
The basic idea is to have use the "External Sites" nextcloud extension
to provide jupyter notebook instances in nextcloud.

1. Install "External Sites"
2. Configure site "jupyter" to point to `<jupyter proxy host>/entry/mysecrettoken/{user}`
3. Setup the jupyter-starter on `<jupyter proxy host>`
4. Manage all the foo in the start/stop/setup scripts
