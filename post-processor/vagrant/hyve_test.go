package vagrant

import (
	"testing"
)

func TestHyveProvider_impl(t *testing.T) {
	var _ Provider = new(HyveProvider)
}
