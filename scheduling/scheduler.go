package scheduling

type Scheduler interface {
	StartAsync()
	Every(interval string, handler func() error) error
}
