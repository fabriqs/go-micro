package payment

var (
	StatusRequiresPaymentMethod = "requires_payment_method"
	StatusRequiresConfirmation  = "requires_confirmation"
	StatusRequiresAction        = "requires_action"
	StatusRequiresCapture       = "requires_capture"
	StatusSucceeded             = "succeeded"
	StatusProcessing            = "processing"
	StatusCanceled              = "canceled"
)

type IntentRequest struct {
	Amount             float64
	Currency           string
	Description        string
	PaymentMethodTypes []string
	CustomerEmail      string
}

type IntentResponse struct {
	Id           string
	ClientSecret string
	Status       string
}

type Provider interface {
	CreateIntent(request *IntentRequest) (*IntentResponse, error)
	GetIntent(ID string) (*IntentResponse, error)
}
