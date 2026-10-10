# Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.
#
# Sourced, not run: reserves a Redis port for one CRE ladder run, so runs in
# any number of terminals -- single scenarios and whole suites side by side --
# never share a Redis. Two engines on one Redis work each other's ladders, and
# the first to finish removes the Redis under the other: four suites started
# together once lost almost every S1-S3 ladder that way.
#
# A reservation is a directory under .run/cre-escalation-ladder/.ports/<port>,
# made with mkdir (atomic), holding the owner's pid. It is taken over only when
# that owner is gone, so a run killed outright does not hold its port forever.
#
#   reserve_port FIRST LAST   prints a free port and reserves it for $$
#   release_ports             frees every port this shell ($$) holds

cre_ports_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/.run/cre-escalation-ladder/.ports"
mkdir -p "${cre_ports_dir}"

reserve_port() {
  local p d owner
  for p in $(seq "$1" "$2"); do
    d="${cre_ports_dir}/${p}"
    if ! mkdir "${d}" 2>/dev/null; then
      owner="$(cat "${d}/pid" 2>/dev/null || true)"
      if [ -z "${owner}" ]; then
        # Somebody made it a moment ago and has not written their pid yet --
        # unless it is minutes old, which is a run killed in between.
        [ -n "$(find "${d}" -maxdepth 0 -mmin +2 2>/dev/null)" ] || continue
      elif kill -0 "${owner}" 2>/dev/null; then
        continue
      fi
      # The owner is gone: move the stale reservation aside (one rename wins)
      # and take the port as if it had been free.
      mv "${d}" "${d}.stale.$$" 2>/dev/null || continue
      rm -rf "${d}.stale.$$"
      mkdir "${d}" 2>/dev/null || continue
    fi
    echo $$ > "${d}/pid"
    # Reserved, but something outside these scripts may still hold it. Keep the
    # reservation (it is ours, and released with the rest) and try the next.
    if docker ps -a --format '{{.Names}}' | grep -qx "cre-ladder-run-redis-${p}" \
       || (exec 3<>"/dev/tcp/127.0.0.1/${p}") 2>/dev/null; then
      continue
    fi
    echo "${p}"
    return 0
  done
  return 1
}

release_ports() {
  local d
  for d in "${cre_ports_dir}"/*; do
    [ -d "${d}" ] || continue
    [ "$(cat "${d}/pid" 2>/dev/null)" = "$$" ] || continue
    rm -f "${d}/pid"
    rmdir "${d}" 2>/dev/null || true
  done
}
