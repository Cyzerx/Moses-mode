#!/bin/sh
# One-shot Compose storage initializer. The server itself always starts as 10001.
set -eu
for dir in /app/data /app/keys /app/skills; do
  [ -d "$dir" ] && [ ! -L "$dir" ] || { echo "Invalid storage directory: $dir" >&2; exit 1; }
done

for dir in /app/data /app/keys; do
  # The mount point protects all old files immediately. Do not follow symlinks,
  # cross filesystems or change owners of files hard-linked outside the mount.
  chmod 0700 "$dir"
  find "$dir" -xdev -type d ! -user 10001 -exec chown 10001:10001 {} +
  find "$dir" -xdev -type f -links 1 ! -user 10001 -exec chown 10001:10001 {} +
done

# Keep the checkout owner's access to operator-editable skills. Only the skills
# mount is group-writable. Application executables are outside all three mounts.
find /app/skills -xdev -type d -exec chgrp 10001 {} + -exec chmod g+rwx,g+s {} +
find /app/skills -xdev -type f -links 1 -exec chgrp 10001 {} + -exec chmod g+rw {} +
