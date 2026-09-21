package link

import (
	"context"
	"errors"
	"fmt"

	"github.com/cyb3rgun/theserver/internal/store"
	"github.com/cyb3rgun/theserver/pkg/protocol"
)

// The calibration side of the link (D-063). A target type says where the
// beacon clusters sit; the server derives the beacon rectangle in canvas
// coordinates from it and tells the device with set_config, so that nobody
// types the four points by hand. A device without a type is told nothing,
// and the setting on the device stays the manual override.

// SendCalibration tells a device the beacon rectangle of its target type
// with set_config and the key calib.rect. It returns what was sent and
// whether anything was: a device without a type, or a type whose layout
// spans no rectangle, is told nothing. A device that is offline gives
// ErrDeviceOffline; it is told again when it connects.
func (s *Server) SendCalibration(ctx context.Context, deviceID string) (store.Calibration, bool, error) {
	calib, ok, err := s.store.DeviceCalibration(ctx, deviceID)
	if err != nil || !ok {
		return store.Calibration{}, false, err
	}
	args := protocol.SetConfig{K: protocol.ConfigCalibRect, V: calib.Value()}
	attrs := []any{"device", deviceID, "target_type", calib.TargetType, protocol.ConfigCalibRect, calib.Value()}
	result, err := s.SendCommand(ctx, deviceID, protocol.CommandSetConfig, args.Args())
	switch {
	case errors.Is(err, ErrDeviceOffline):
		s.log.Info("calibration waits for the device", attrs...)
		return calib, false, err
	case err != nil:
		s.log.Warn("calibration failed", append(attrs, "error", err)...)
		return calib, false, err
	case !result.OK:
		s.log.Warn("calibration refused", append(attrs, "error", result.E)...)
		return calib, false, fmt.Errorf("%s: %s: %w: %s", deviceID, protocol.ConfigCalibRect, ErrRefused, result.E)
	}
	s.log.Info("calibration sent", attrs...)
	return calib, true, nil
}

// calibrationOnConnect tells a device that just connected where the beacons
// of its type sit, before it is told what it plays (D-063).
func (c *deviceConn) calibrationOnConnect() {
	if _, _, err := c.srv.SendCalibration(c.ctx, c.deviceID); err != nil && !errors.Is(err, ErrDeviceOffline) {
		c.log.Warn("could not send the calibration on connect", "error", err)
	}
}
