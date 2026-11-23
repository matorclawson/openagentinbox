package dostoevsky_test

import (
	"fmt"
	d "github.com/rodriveracom/dostoevsky"
)

func ExampleNotifications() {
	changes := d.NewNotifications[d.Partition](64)
	orders := d.Partition{Topic: "orders", Number: 0}
	// Subscribe before checking storage so a concurrent commit cannot be missed.
	changed, release, err := changes.Subscribe(orders)
	if err != nil {
		panic(err)
	}
	defer release()

	// The producer calls Signal after the new record is durable.
	changes.Signal(orders)
	<-changed
	fmt.Println("read orders partition 0 again")
	// Output: read orders partition 0 again
}
