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
		title:   localize.T("ClashPulse connection health"),
		message: fmt.Sprintf(localize.T("All measured connections are slower than %d ms"), threshold.Milliseconds()),
	}
}

func lowerLatencyNotification() desktopNotification {
	return desktopNotification{title: localize.T("Connection improved"), message: localize.T("Switched to a lower-latency connection")}
}

func testNotification() desktopNotification {
	return desktopNotification{title: localize.T("ClashPulse notifications"), message: localize.T("Notifications are working")}
}

func notifyDesktop(notification desktopNotification) error {
	beeep.AppName = "ClashPulse"
	return beeep.Notify(notification.title, notification.message, "")
}
