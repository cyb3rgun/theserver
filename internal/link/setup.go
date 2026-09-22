package link

import (
	"context"
	"errors"
	"fmt"

	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// What a device is told about itself (D-063, D-068). A target type says
// where its beacon clusters sit, and the room says how the beacon round is
// divided; the server turns both into settings and sends them with
// set_config, so that nobody types a point or a slot by hand. A device
// without a type hears nothing about its beacons, one without a room
// nothing about the plan, and the settings on the device stay the manual
// override.

// SendSetup tells a device the points of its target type, the rectangle
// around them and the beacon plan of its room with its own slot. It returns
// the setup and whether the device took it: a device with nothing to be told
// gives false without an error, and one that is offline gives
// ErrDeviceOffline and is told again when it connects.
func (s *Server) SendSetup(ctx context.Context, deviceID string) (store.Setup, bool, error) {
	setup, err := s.store.DeviceSetup(ctx, deviceID)
	if err != nil {
		return store.Setup{}, false, err
	}
	settings := setup.Settings()
	if len(settings) == 0 {
		return setup, false, nil
	}
	for _, setting := range settings {
		args := protocol.SetConfig{K: setting.Key, V: setting.Value}
		attrs := []any{"device", deviceID, "key", setting.Key, "value", setting.Value}
		result, err := s.SendCommand(ctx, deviceID, protocol.CommandSetConfig, args.Args())
		switch {
		case errors.Is(err, ErrDeviceOffline):
			s.log.Info("setup waits for the device", attrs...)
			return setup, false, err
		case err != nil:
			s.log.Warn("setup failed", append(attrs, "error", err)...)
			return setup, false, err
		case !result.OK:
			s.log.Warn("setup refused", append(attrs, "error", result.E)...)
			return setup, false, fmt.Errorf("%s: %s: %w: %s", deviceID, setting.Key, ErrRefused, result.E)
		}
		s.log.Info("setting sent", attrs...)
	}
	return setup, true, nil
}

// setupOnConnect tells a device that just connected what it is and where it
// stands, before it is told what it plays (D-063, D-068).
func (c *deviceConn) setupOnConnect() {
	if _, _, err := c.srv.SendSetup(c.ctx, c.deviceID); err != nil && !errors.Is(err, ErrDeviceOffline) {
		c.log.Warn("could not send the setup on connect", "error", err)
	}
}
