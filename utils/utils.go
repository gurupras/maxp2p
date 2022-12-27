package utils

import "net/url"

// CreateDeviceIDQuery creates a URL object that contains the passed in deviceID
func CreateDeviceIDQuery(id, urlStr string) string {
	u, _ := url.Parse(urlStr)
	q := u.Query()
	q.Set("deviceID", id)
	u.RawQuery = q.Encode()
	return u.String()
}
