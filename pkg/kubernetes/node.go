package kubernetes

// NodeInterface handles node commands
type NodeInterface interface{}

// kubernetesNodeInterface implements the NodeInterface using the standard golang client
type kubernetesNodeInterface struct{}
