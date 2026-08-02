// Package store reads and writes the on-disk, git-native workspace format
// (byte-stable .qv.yaml files), handles schema versioning and migrations, and
// provides git integration plus import/export. It is the projection of the model
// onto the filesystem.
package store
