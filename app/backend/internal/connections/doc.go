// Package connections keeps the owner's Usenet servers and debrid accounts
// in one place and pushes them to the apps that use them: Usenet servers to
// SABnzbd through its API, debrid accounts to Decypharr through its
// config file and a restart.
//
// Everything is stored as one encrypted secret setting, so the module owns
// no table and needs no migration. Secrets never leave the server after they
// are saved; views carry secrets.Masked instead.
//
// Entries made directly in SABnzbd or Decypharr are listed as unmanaged and
// never changed or deleted. The Decypharr writer refuses to touch a config
// whose bind address is not 127.0.0.1 or whose Hearsay switches are not all
// off, and it never writes those keys itself.
package connections
