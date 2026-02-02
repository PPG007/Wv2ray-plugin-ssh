# Wv2ray-plugin-ssh

[中文](./README_zh.md)

This repository is a Wv2ray plugin which supports forward requests through SSH dynamic port forwarding.

The plugin is powered by [go-plugin](https://github.com/hashicorp/go-plugin) and based on [grpc](https://grpc.io/)，so the plugin can be writtern in any language that supports grpc. This repository is written in golang.

## Architecture

![architecture](./architecture.png)

When a proxy request comes in, the inbound server will process the request and forward it to the outbound server. If the request uses a protocol that is not supported in xray, the plugin outbound server will handle it.

In the plugin outbound server, this handler will build a connection to the proxy server using the protocol, address, port and other parameters. After the connection is established, the plugin outbound server will call the CreateHandler&Handshake grpc method to create a handler and handshake with the proxy server.

After Handshake ready, the plugin outbound server will call the Process grpc method to process the request.And during the process, the Handshake stream should not be closed to forward data between the plugin and the proxy server(Virtual Connection in the image).

When Process method is returned, the plugin outbound server will close all streams and call Shutdown method.
