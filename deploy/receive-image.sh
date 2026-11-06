#!/usr/bin/env bash
set -euo pipefail

if [[ ! ${SSH_ORIGINAL_COMMAND:-} =~ ^deploy\ ([a-f0-9]{40})$ ]]; then
    printf 'Expected a deployment commit SHA\n' >&2
    exit 1
fi
commit=${BASH_REMATCH[1]}

exec 9>/run/lock/fx-quotes-deploy.lock
flock 9
cd /opt/fx-quotes
git fetch --no-tags origin refs/heads/main </dev/null
if [[ $(git rev-parse FETCH_HEAD) != "$commit" ]]; then
    printf 'Deployment commit differs from origin main\n' >&2
    exit 1
fi

export FX_APP_IMAGE="fx-quotes-deploy:$commit"
docker load
docker image inspect "$FX_APP_IMAGE" >/dev/null
git checkout --detach "$commit"
docker compose --env-file /etc/fx-quotes/runtime.env \
    -f docker-compose.yaml -f /etc/fx-quotes/compose.deploy.yaml up --no-build -d

for ((attempt = 0; attempt < 20; attempt++)); do
    status=$(curl -s --max-time 2 -o /dev/null -w '%{http_code}' 'http://127.0.0.1:3000/quotes/latest?pair=EUR%2FMXN' || true)
    if [[ $status == 200 || $status == 404 ]]; then
        printf 'API ready on port 3000\n'
        exit 0
    fi
    sleep 1
done
printf 'API did not become ready\n' >&2
docker compose --env-file /etc/fx-quotes/runtime.env \
    -f docker-compose.yaml -f /etc/fx-quotes/compose.deploy.yaml logs --tail=50 app migrate >&2
exit 1
