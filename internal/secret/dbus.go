package secret

import (
	"fmt"

	"github.com/godbus/dbus/v5"
)

const (
	ssBus             = "org.freedesktop.secrets"
	ssServicePath     = "/org/freedesktop/secrets"
	ssServiceIface    = "org.freedesktop.Secret.Service"
	ssCollectionIface = "org.freedesktop.Secret.Collection"
	ssItemIface       = "org.freedesktop.Secret.Item"
	ssDefaultAlias    = "default"
	ssItemAttrs       = "org.freedesktop.Secret.Item.Attributes"
	ssItemLabel       = "org.freedesktop.Secret.Item.Label"
)

var attrs = map[string]string{"service": "tori", "account": "torbox-api"}

type ssSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

// DBus is a libsecret adapter over org.freedesktop.secrets.
type DBus struct {
	connect func() (*dbus.Conn, error)
}

func NewDBus() *DBus {
	return &DBus{connect: func() (*dbus.Conn, error) { return dbus.ConnectSessionBus() }}
}

func (d *DBus) Lookup() (Key, error) {
	conn, err := d.connect()
	if err != nil {
		return Key{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	svc := conn.Object(ssBus, ssServicePath)
	var unlocked, locked []dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".SearchItems", 0, attrs).Store(&unlocked, &locked); err != nil {
		return Key{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	items := append(append([]dbus.ObjectPath{}, unlocked...), locked...)
	if len(items) == 0 {
		return Key{}, ErrNotFound
	}
	session, err := openSession(svc)
	if err != nil {
		return Key{}, err
	}
	if len(locked) > 0 {
		_ = svc.Call(ssServiceIface+".Unlock", 0, locked)
	}
	secrets := map[dbus.ObjectPath]ssSecret{}
	if err := svc.Call(ssServiceIface+".GetSecrets", 0, items, session).Store(&secrets); err != nil {
		return Key{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	for _, sec := range secrets {
		if k, err := NewKey(string(sec.Value)); err == nil && !k.Empty() {
			return k, nil
		}
	}
	return Key{}, ErrNotFound
}

func (d *DBus) Save(k Key) error {
	conn, err := d.connect()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	svc := conn.Object(ssBus, ssServicePath)
	session, err := openSession(svc)
	if err != nil {
		return err
	}
	collPath, err := defaultCollection(svc)
	if err != nil {
		return err
	}
	props := map[string]dbus.Variant{
		ssItemLabel: dbus.MakeVariant("tori: TorBox API key"),
		ssItemAttrs: dbus.MakeVariant(attrs),
	}
	sec := ssSecret{Session: session, Value: []byte(k.v), ContentType: "text/plain"}
	var item, prompt dbus.ObjectPath
	if err := conn.Object(ssBus, collPath).Call(ssCollectionIface+".CreateItem", 0, props, sec, true).Store(&item, &prompt); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return nil
}

func (d *DBus) Delete() error {
	conn, err := d.connect()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	svc := conn.Object(ssBus, ssServicePath)
	var unlocked, locked []dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".SearchItems", 0, attrs).Store(&unlocked, &locked); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	items := append(unlocked, locked...)
	if len(items) == 0 {
		return ErrNotFound
	}
	for _, p := range items {
		var prompt dbus.ObjectPath
		if err := conn.Object(ssBus, p).Call(ssItemIface+".Delete", 0).Store(&prompt); err != nil {
			return fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
	}
	return nil
}

func openSession(svc dbus.BusObject) (dbus.ObjectPath, error) {
	var out dbus.Variant
	var session dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &session); err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return session, nil
}

func defaultCollection(svc dbus.BusObject) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath
	if err := svc.Call(ssServiceIface+".ReadAlias", 0, ssDefaultAlias).Store(&path); err == nil && path != "" && path != "/" {
		return path, nil
	}
	return "/org/freedesktop/secrets/collection/login", nil
}
