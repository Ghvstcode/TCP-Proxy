
**What is this?**

This is a configurable TCP proxy that proxys connections from ports to targets as specified in a `config.json` file. The configuration file has the construct of an "app" Which has  a name, an array of ports and an array of targets.
```
{  
  "Apps": [  
    {  
	    "Name": "app name",  
      "Ports": [  
        5001,  
        5200,  
        5300,  
        5400  
      ],  
      "Targets": [  
        "echo-server:1234",  
        "echo-server:5678"  
      ]  
    }
```
This proxy listens on all the ports for all the provided apps and forwards connections to their corresponding targets.

**Demo**

![ezgif-5-c1d4a5c96b](https://user-images.githubusercontent.com/46195831/204164196-070c3756-46fb-45ee-b4e0-a9e49a89c218.gif)

  **Features**

  These are some features of this project
  Healthchecks on Targets — A periodic health check is done all the various app targets to ensure connection on an apps ports gets forwarded to "healthy" targets. The logic for the health check lives here<link to code line> ! It runs in a sepreate goroutine & the `healthcheckItr` runs every 30 seconds. `healthcheckItr` iterates over all the backends on the server pool and checks if a target is alive by pinging it. The timeout period is 2 seconds. if we receive an error from the ping, we mark that particular "backend" as dead.
  For every app, The health check for its targets are ran in a seperate goroutine. App A's target healthcheck runs in its own goroutine, App B's target healthcheck runs in its own goroutine, et cetra.

Load-balancing — There is a basic round-robin balancing happening on the app level between the various targets which have been marked live in the step above. I imagine a better balancing technique could be using the least connection technique to efficiently distribute traffic across the backends.

Listening on all available ports — As we saw above, It is possible for an app to have various ports & for our config file to contain various apps. It is a requirement for this tool to accept connections on all the ports for every app listed in the configuration file. How do we ensure the proxy is able to
1. Accept connections on all the ports listed in the config file
2. Associate the incoming connections on a said port to an app(So it can forward connections to the right target)
3. Gracefully close all the listeners on proxy shutdown.
   I got an idea from a pattern I'd seen in the Go standard library — The [http.Server](https://cs.opensource.google/go/go/+/refs/tags/go1.19.3:src/net/http/server.go;l=26890) ! The Server struct has a private field listeners which is a map with `net.Listener` as a key! That inspired the similarly named field on the Server struct in my implementation!
```golang
type Server struct {  
   listener      map[*net.Listener]string  
   ......
}
```
The listener field is a map with a pointer to the listener as the key and the name of the app as the value.  When we run the start the program, Right after parsing the JSON configuration file, we iterate over all the apps and all their ports, creating listeners and setting the app name.

```golang
for _, port := range val.Ports {  
   l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))  
   if err != nil {  
      log.Println(err)  
   }  
   s.listener[&l] = val.Name  
}
```
Later on, when we receieve a connection that we need to proxy, The app name we set above becomes the key we use to retrieve the target for a specific application! Eventually, we need to shutdown the server, We iterate over all the various listeners and close them.
```golang
for ln := range s.listener {  
   if err := (*ln).Close(); err != nil {  
      log.Fatal("Error occurred while closing listener", err)  
   }}
   
```
Another field present on the server struct is `Quit` which is a custom SigChannel type. The SigChannel type is essentailly a wrapper around the channel with a `sync.Once` present to ensure that whenever the channel is closed, it is done only once to prevent panics.
```golang
type SigChannel struct {  
   C      chan os.Signal   
   once   sync.Once  
}
```
Whenever the Quit channel receives a SIGTERM, it starts the process of shutting down the servers!
