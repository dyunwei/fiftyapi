// Package configenv reads FiftyAPI environment configuration.
package configenv

import "os"

func Value(name string) string {
	return os.Getenv(name)
}
