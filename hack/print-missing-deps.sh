set -o errexit
set -o nounset
set -o pipefail

echo "Verifying Docker Executables have appropriate dependencies"

printMissingDep() {
  if /usr/bin/ldd "$@" | grep "not found"; then
    echo "!!! Missing deps for $@ !!!"
    exit 1
  fi
}

export -f printMissingDep

/usr/bin/find / -type f -executable -print | /usr/bin/xargs -I {} /bin/bash -c 'printMissingDep "{}"'