#!/bin/sh

mkdir -p /root/tailscale
# start tailscaled
echo "starting tailscaled"
/app/tailscaled --statedir=/root/tailscale --socket=/tmp/tailscale.sock &

if [ ! -z "${AUTH_KEY}" ]; then
    export AUTH="--authkey=${AUTH_KEY}"
    echo "Bringing tailscale interface up with authkey"
else
    echo "Bringing tailscale interface up"
fi

/app/tailscale --socket=/tmp/tailscale.sock up ${AUTH} --hostname=${HOSTNAME} --ssh

/app/quis-custodiet
