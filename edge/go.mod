// The edge agent is its own module on purpose: it is deployed to cheap boxes we
// don't fully trust the network path to, so it should be a small static binary
// with no database driver and no control-plane code in it at all.
module github.com/treykys/proxify-vpn/edge

go 1.25.0
