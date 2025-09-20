package main

import "context"

type consumerRunner struct{}

func registerConsumers() *consumerRunner {
	return &consumerRunner{}
}

func (cr *consumerRunner) start(_ context.Context) error {
	return nil
}
