package micro

type Scheduler interface {
	StartAsync()
	Every(interval string, handler func(ctx Ctx) error) error
}
