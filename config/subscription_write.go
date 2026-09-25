package config

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/BurntSushi/toml"
)

type SubscriptionEdit struct {
	Name            *string
	URL             *string
	UserAgent       *string
	Enabled         *bool
	RefreshInterval *time.Duration
	Timeout         *time.Duration
	Route           *string
	AllowHTTP       *bool
	AllowInvalidTLS *bool
}

// PatchSubscription changes only supplied user-intent fields. An absent URL
// retains the private source; adding a new entry requires one.
func PatchSubscription(path string, current Snapshot, id string, patch SubscriptionEdit) error {
	var document subscriptionsDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	next := cloneSnapshot(current)
	index := -1
	for i := range document.Subscriptions {
		if document.Subscriptions[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		if patch.URL == nil {
			return errors.New("subscriptions.toml: subscription.url: required for new subscription")
		}
		document.Subscriptions = append(document.Subscriptions, subscriptionDoc{ID: id})
		next.Subscriptions = append(next.Subscriptions, Subscription{ID: id})
		index = len(document.Subscriptions) - 1
	}
	item := &document.Subscriptions[index]
	state := &next.Subscriptions[index]
	if patch.Name != nil {
		item.Name, state.Name = *patch.Name, *patch.Name
	}
	if patch.URL != nil {
		item.URL, state.URL = *patch.URL, *patch.URL
	}
	if patch.UserAgent != nil {
		item.UserAgent, state.UserAgent = *patch.UserAgent, *patch.UserAgent
	}
	if patch.Enabled != nil {
		item.Enabled, state.Enabled = *patch.Enabled, *patch.Enabled
	}
	if patch.Route != nil {
		item.Route, state.Route = *patch.Route, *patch.Route
	}
	if patch.AllowHTTP != nil {
		item.AllowHTTP, state.AllowHTTP = *patch.AllowHTTP, *patch.AllowHTTP
	}
	if patch.AllowInvalidTLS != nil {
		item.AllowInvalidTLS, state.AllowInvalidTLS = *patch.AllowInvalidTLS, *patch.AllowInvalidTLS
	}
	if patch.RefreshInterval != nil {
		interval := patch.RefreshInterval.String()
		item.RefreshInterval = &interval
		state.RefreshInterval = *patch.RefreshInterval
	}
	if patch.Timeout != nil {
		timeout := patch.Timeout.String()
		item.Timeout = &timeout
		state.Timeout = *patch.Timeout
	}
	applyDefaults(&next)
	if err := validateSnapshot(next); err != nil {
		return err
	}
	if reflect.DeepEqual(next, current) {
		return nil
	}
	return writeSubscriptions(path, document)
}

func DeleteSubscription(path string, current Snapshot, id string) error {
	var document subscriptionsDoc
	if err := decodeStrict(path, &document); err != nil {
		return err
	}
	found := false
	document.Subscriptions = slices.DeleteFunc(document.Subscriptions, func(item subscriptionDoc) bool {
		if item.ID == id {
			found = true
			return true
		}
		return false
	})
	if !found {
		return os.ErrNotExist
	}
	next := cloneSnapshot(current)
	next.Subscriptions = slices.DeleteFunc(next.Subscriptions, func(item Subscription) bool { return item.ID == id })
	if err := validateSnapshot(next); err != nil {
		return err
	}
	return writeSubscriptions(path, document)
}

func writeSubscriptions(path string, document subscriptionsDoc) error {
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(document); err != nil {
		return err
	}
	return Write(path, encoded.Bytes())
}
