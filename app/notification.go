package app

import (
	"fmt"
	"time"

	"github.com/fishman/clashpulse/localize"
	"github.com/gen2brain/beeep"
)

type desktopNotification struct {
	title, message string
}

func slowConnectionsNotification(threshold time.Duration) desktopNotification {
	return desktopNotification{
		title:   "ClashPulse connection health",
		message: fmt.Sprintf("All measured connections are slower than %d ms", threshold.Milliseconds()),
	}
}

func lowerLatencyNotification() desktopNotification {
	return desktopNotification{title: localize.T("Connection improved"), message: localize.T("Switched to a lower-latency connection")}
}

func notifyDesktop(notification desktopNotification) error {
	beeep.AppName = "ClashPulse"
	return beeep.Notify(notification.title, notification.message, "")
}
