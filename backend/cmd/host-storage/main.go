// host-storage runs on the Mac, independently of the document I/O path.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/pinkhit/vault/backend/internal/hoststorage"
)

// The path is an argument, never interpolated into JavaScript. Foundation
// measures the volume containing Docker's disk image, including external disks.
const measurement = `ObjC.import('Foundation');
function run(argv) {
  const url = $.NSURL.fileURLWithPath(argv[0]);
  const keys = $([$.NSURLVolumeTotalCapacityKey, $.NSURLVolumeAvailableCapacityKey,
    $.NSURLVolumeAvailableCapacityForImportantUsageKey]);
  const values = ObjC.deepUnwrap(url.resourceValuesForKeysError(keys, null));
  return JSON.stringify({total_bytes: values.NSURLVolumeTotalCapacityKey,
    free_bytes: values.NSURLVolumeAvailableCapacityKey,
    available_including_reclaimable_bytes: values.NSURLVolumeAvailableCapacityForImportantUsageKey});
}`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if runtime.GOOS != "darwin" {
		return errors.New("host-storage must run on macOS, outside Docker")
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	disk := flag.String("docker-disk", filepath.Join(userHome, "Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw"), "Docker Desktop disk-image path (operator configuration only)")
	output := flag.String("output-dir", ".runtime/host-storage", "snapshot directory")
	agent := flag.Bool("print-launch-agent", false, "print a user-level macOS timer configuration")
	flag.Parse()
	if *agent {
		binary, err := os.Executable()
		if err != nil {
			return err
		}
		directory, err := filepath.Abs(*output)
		if err != nil {
			return err
		}
		fmt.Printf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.familyvault.host-storage</string>
<key>ProgramArguments</key><array><string>%s</string><string>-output-dir</string><string>%s</string><string>-docker-disk</string><string>%s</string></array>
<key>RunAtLoad</key><true/><key>StartInterval</key><integer>30</integer>
<key>ProcessType</key><string>Background</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, html.EscapeString(binary), html.EscapeString(directory), html.EscapeString(*disk), html.EscapeString(filepath.Join(filepath.Dir(directory), "host-storage.log")))
		return nil
	}
	if info, err := os.Stat(*disk); err != nil || !info.Mode().IsRegular() {
		return errors.New("Docker disk image not found; set -docker-disk to its location in Docker Desktop settings")
	}
	if err = os.MkdirAll(*output, 0755); err != nil {
		return err
	}
	// A single publisher owns this snapshot directory.
	lock, err := os.OpenFile(filepath.Join(*output, ".producer.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("a host-storage collector is already running")
	}
	// launchd owns the schedule; each invocation publishes one complete sample.
	sample, err := collect(context.Background(), *disk)
	if publishErr := hoststorage.Publish(*output, sample); publishErr != nil {
		return publishErr
	}
	return err
}

func collect(ctx context.Context, disk string) (*hoststorage.Sample, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", measurement, disk)
	command.WaitDelay = time.Second
	data, err := command.Output() // No inherited stderr or document data.
	if err != nil {
		return nil, errors.New("macOS capacity measurement failed")
	}
	var sample hoststorage.Sample
	if len(data) > 4096 || json.Unmarshal(data, &sample) != nil {
		return nil, errors.New("invalid macOS measurement")
	}
	sample.Version, sample.CollectedAt = 1, time.Now().UTC()
	if !sample.Valid(time.Now()) {
		return nil, errors.New("inconsistent macOS measurement")
	}
	return &sample, nil
}
