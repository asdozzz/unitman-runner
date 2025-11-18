#!/bin/bash

# Directory to save the tar files
SAVE_DIR="./podman_images_backup"
mkdir -p "$SAVE_DIR"

# Get a list of all image IDs
IMAGES=$(podman images --format "{{.Repository}}:{{.Tag}}" | grep -v "$1")

# Loop through each image ID and save it to a separate tar file
for IMAGE_INFO in $IMAGES; do
  # Sanitize the filename to remove characters that might cause issues
  FILENAME=$(echo "$IMAGE_INFO" | sed 's/[^a-zA-Z0-9_.-]/_/g')
  rm -rf $SAVE_DIR/$FILENAME.tar
  echo "Saving image $IMAGE_INFO to $SAVE_DIR/$FILENAME.tar"
  podman save -o "$SAVE_DIR/$FILENAME.tar" "$IMAGE_INFO"
done

echo "All images saved to individual tar files in $SAVE_DIR"
