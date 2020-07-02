package notification

import "fmt"

// Notifier handles sending notifications
type Notifier interface {
	// Notify sends a notification indicating success or failure
	Notify(success bool, err error, nodes []string) error
}

// makeMessageString forms the message string
func makeMessageString(success bool, err error, nodes []string) string {
	var msg string
	if success {
		msg = fmt.Sprintf("successfully deleted nodes %v", nodes)
	} else {
		msg = fmt.Sprintf("error %v", err)
	}

	return fmt.Sprintf("Grim-Reaper: %v", msg)
}
