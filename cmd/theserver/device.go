package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/cyb3rgun/theserver/internal/store"
)

// runDevice manages devices from the command line (D-019). Tokens are issued
// here in S01; the admin UI takes this over later.
func runDevice(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "theserver: device needs add, list, reset or revoke\n\n%s", usage)
		return 2
	}
	switch args[0] {
	case "add":
		return deviceAdd(args[1:], stdout, stderr)
	case "list":
		return deviceList(args[1:], stdout, stderr)
	case "reset":
		return deviceReset(args[1:], stdout, stderr)
	case "revoke":
		return deviceRevoke(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "theserver: unknown device command %q\n\n%s", args[0], usage)
		return 2
	}
}

var (
	deviceKinds   = []string{store.KindTarget, store.KindController, store.KindBridge}
	deviceClasses = []string{store.ClassESP, store.ClassPi, store.ClassPC}
)

func deviceAdd(args []string, stdout, stderr io.Writer) int {
	fs, configPath := newFlagSet("device add", stderr)
	id := fs.String("id", "", "device id, for example tgt-01")
	kind := fs.String("kind", "", "target, controller or bridge")
	class := fs.String("class", store.ClassESP, "esp, pi or pc")
	name := fs.String("name", "", "display name")
	room := fs.String("room", "", "room the device is in")
	zone := fs.String("zone", "", "radio zone of the device")
	if code := parse(fs, args, stderr); code >= 0 {
		return code
	}
	switch {
	case *id == "":
		fmt.Fprintln(stderr, "theserver: device add needs --id")
		return 2
	case !slices.Contains(deviceKinds, *kind):
		fmt.Fprintf(stderr, "theserver: --kind must be one of %v\n", deviceKinds)
		return 2
	case !slices.Contains(deviceClasses, *class):
		fmt.Fprintf(stderr, "theserver: --class must be one of %v\n", deviceClasses)
		return 2
	}

	ctx := context.Background()
	db, code := openFromFlags(ctx, fs, *configPath, stderr)
	if db == nil {
		return code
	}
	defer db.Close()

	if _, err := db.GetDevice(ctx, *id); err == nil {
		fmt.Fprintf(stderr, "theserver: device %s already exists\n", *id)
		return 1
	} else if !errors.Is(err, store.ErrDeviceNotFound) {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}

	token, err := store.NewDeviceToken()
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	err = db.UpsertDevice(ctx, store.Device{
		ID: *id, Kind: *kind, Class: *class, Name: *name, Room: *room, Zone: *zone,
		Status: store.StatusApproved, TokenHash: store.HashToken(token),
	})
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}

	fmt.Fprintf(stdout, "device %s added: kind %s, class %s, status %s\n", *id, *kind, *class, store.StatusApproved)
	fmt.Fprintf(stdout, "token: %s\n", token)
	fmt.Fprintln(stdout, "warning: this token is shown once and cannot be shown again; put it into the device now")
	return 0
}

func deviceList(args []string, stdout, stderr io.Writer) int {
	fs, configPath := newFlagSet("device list", stderr)
	if code := parse(fs, args, stderr); code >= 0 {
		return code
	}
	ctx := context.Background()
	db, code := openFromFlags(ctx, fs, *configPath, stderr)
	if db == nil {
		return code
	}
	defer db.Close()

	devices, err := db.ListDevices(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	table := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tKIND\tCLASS\tSTATUS\tEPOCH\tLAST SEEN")
	for _, d := range devices {
		lastSeen := "never"
		if d.LastSeen != 0 {
			lastSeen = time.UnixMilli(d.LastSeen).Local().Format(time.DateTime)
		}
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\t%s\n", d.ID, d.Kind, d.Class, d.Status, d.SeqEpoch, lastSeen)
	}
	if err := table.Flush(); err != nil {
		return 1
	}
	if len(devices) == 0 {
		fmt.Fprintln(stdout, "no devices yet; add one with: theserver device add --id <id> --kind target")
	}
	return 0
}

// deviceReset starts a new sequence epoch for a device (D-026), for a device
// that lost its counter or has to start its journal over.
func deviceReset(args []string, stdout, stderr io.Writer) int {
	fs, configPath := newFlagSet("device reset", stderr)
	idFlag := fs.String("id", "", "device id")
	id, code := parseWithID(fs, args, idFlag, stderr)
	if code >= 0 {
		return code
	}
	if id == "" {
		fmt.Fprintln(stderr, "theserver: device reset needs the device id")
		return 2
	}
	ctx := context.Background()
	db, code := openFromFlags(ctx, fs, *configPath, stderr)
	if db == nil {
		return code
	}
	defer db.Close()

	epoch, err := db.ResetDevice(ctx, id)
	if err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "device %s reset: epoch %d, it starts over at seq 1; the events of earlier epochs stay\n", id, epoch)
	fmt.Fprintln(stdout, "a running server closes its connection within a second, and the device learns the epoch when it connects again")
	return 0
}

func deviceRevoke(args []string, stdout, stderr io.Writer) int {
	fs, configPath := newFlagSet("device revoke", stderr)
	id := fs.String("id", "", "device id")
	if code := parse(fs, args, stderr); code >= 0 {
		return code
	}
	if *id == "" {
		fmt.Fprintln(stderr, "theserver: device revoke needs --id")
		return 2
	}
	ctx := context.Background()
	db, code := openFromFlags(ctx, fs, *configPath, stderr)
	if db == nil {
		return code
	}
	defer db.Close()

	if err := db.SetStatus(ctx, *id, store.StatusBlocked); err != nil {
		fmt.Fprintf(stderr, "theserver: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "device %s revoked: status %s; a running server closes its connection within a second\n", *id, store.StatusBlocked)
	return 0
}
