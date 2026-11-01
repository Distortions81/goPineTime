//go:build !pinetime

package main

import "errors"

type unavailableHealthDevice struct{}

func newHealthDevice() healthDevice                 { return unavailableHealthDevice{} }
func (unavailableHealthDevice) SetHeart(bool) error { return errors.New("heart sensor unavailable") }
func (unavailableHealthDevice) ReadHeart() (uint16, error) {
	return 0, errors.New("heart sensor unavailable")
}
func (unavailableHealthDevice) SetSteps(bool) error { return errors.New("step sensor unavailable") }
func (unavailableHealthDevice) ReadSteps() (uint32, error) {
	return 0, errors.New("step sensor unavailable")
}
