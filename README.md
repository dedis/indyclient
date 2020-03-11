# indyclient

An (almost) pure Go client for the Indy blockchain

It does depend on package github.com/pebbe/zmq4, which uses
cgo to call into libzmq. All of the Indy-specific messages
and (eventually) crypto will stay in Go, as opposed to
linking against libindy.



