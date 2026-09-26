// BenchServer is the NDJSON/TCP transport. Each connected client sends one JSON
// request per line; requests are marshalled onto the Unity main thread (via
// TestbenchPlugin.RunOnMain), executed there, and the response is written back.
//
// See docs/ingame-testbench.md for the protocol.

using System;
using System.Collections.Generic;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;
using System.Threading;
using Newtonsoft.Json;
using Newtonsoft.Json.Linq;
using UnityEngine;

namespace Ic10Go.Testbench
{
    internal sealed class BenchServer
    {
        private readonly string _host;
        private readonly int _port;
        private readonly TestbenchPlugin _plugin;
        private readonly List<Client> _clients = new List<Client>();
        private readonly object _clientsLock = new object();

        private TcpListener _listener;
        private volatile bool _running;

        public BenchServer(string host, int port, TestbenchPlugin plugin)
        {
            _host = host;
            _port = port;
            _plugin = plugin;
        }

        public void Start()
        {
            _running = true;
            _listener = new TcpListener(IPAddress.Parse(_host), _port);
            _listener.Start();
            var t = new Thread(AcceptLoop) { IsBackground = true, Name = "ic10go-testbench-accept" };
            t.Start();
        }

        public void Stop()
        {
            _running = false;
            try { _listener?.Stop(); } catch { }
            lock (_clientsLock)
            {
                foreach (var c in _clients) c.Close();
                _clients.Clear();
            }
        }

        private void AcceptLoop()
        {
            while (_running)
            {
                TcpClient tcp;
                try { tcp = _listener.AcceptTcpClient(); }
                catch { if (!_running) return; continue; }
                var client = new Client(this, tcp);
                lock (_clientsLock) _clients.Add(client);
                client.Start();
            }
        }

        internal void Remove(Client client)
        {
            lock (_clientsLock) _clients.Remove(client);
        }

        /// <summary>Sends an unsolicited event (e.g. watch state) to every client.</summary>
        public void Broadcast(JObject ev)
        {
            lock (_clientsLock)
                foreach (var c in _clients) c.Send(ev);
        }

        /// <summary>Marshals a request to the main thread and returns the response.</summary>
        internal JObject Dispatch(JObject req)
        {
            JToken id = req["id"];
            string cmd = (string)req["cmd"];
            if (string.IsNullOrEmpty(cmd)) return Error(id, "bad-request", "missing \"cmd\"");
            var args = req["args"] as JObject ?? new JObject();

            JObject result = null;
            BenchError error = null;
            using (var done = new ManualResetEventSlim(false))
            {
                _plugin.RunOnMain(() =>
                {
                    try { result = _plugin.Dispatch(cmd, args); }
                    catch (BenchError e) { error = e; }
                    catch (Exception e) { error = new BenchError("internal", e.Message); }
                    finally { done.Set(); }
                });
                if (!done.Wait(TimeSpan.FromSeconds(60)))
                    return Error(id, "timeout", "command timed out on the main thread");
            }

            if (error != null) return Error(id, error.Code, error.Message);
            return new JObject
            {
                ["id"] = id ?? JValue.CreateNull(),
                ["ok"] = true,
                ["result"] = result ?? new JObject(),
            };
        }

        private static JObject Error(JToken id, string code, string message)
        {
            return new JObject
            {
                ["id"] = id ?? JValue.CreateNull(),
                ["ok"] = false,
                ["error"] = new JObject { ["code"] = code, ["message"] = message },
            };
        }

        /// <summary>One connected client; reads requests and writes responses.</summary>
        internal sealed class Client
        {
            private readonly BenchServer _server;
            private readonly TcpClient _tcp;
            private readonly NetworkStream _stream;
            private readonly object _writeLock = new object();
            private volatile bool _closed;

            public Client(BenchServer server, TcpClient tcp)
            {
                _server = server;
                _tcp = tcp;
                _stream = tcp.GetStream();
            }

            public void Start()
            {
                var t = new Thread(Loop) { IsBackground = true, Name = "ic10go-testbench-client" };
                t.Start();
            }

            private void Loop()
            {
                try
                {
                    Send(new JObject
                    {
                        ["event"] = "hello",
                        ["mod"] = TestbenchPlugin.ModId,
                        ["version"] = TestbenchPlugin.ModVersion,
                    });
                    using (var reader = new StreamReader(_stream, new UTF8Encoding(false), false, 8192, true))
                    {
                        string line;
                        while ((line = reader.ReadLine()) != null)
                        {
                            line = line.Trim();
                            if (line.Length == 0) continue;
                            JObject req;
                            try { req = JObject.Parse(line); }
                            catch (Exception ex)
                            {
                                Send(Error(null, "bad-request", "invalid JSON: " + ex.Message));
                                continue;
                            }
                            Send(_server.Dispatch(req));
                        }
                    }
                }
                catch (Exception ex)
                {
                    if (!_closed) Debug.Log("[" + TestbenchPlugin.ModId + "] client closed: " + ex.Message);
                }
                finally
                {
                    Close();
                    _server.Remove(this);
                }
            }

            public void Send(JObject o)
            {
                if (_closed) return;
                try
                {
                    byte[] bytes = Encoding.UTF8.GetBytes(o.ToString(Formatting.None) + "\n");
                    lock (_writeLock)
                    {
                        _stream.Write(bytes, 0, bytes.Length);
                        _stream.Flush();
                    }
                }
                catch { /* client went away; the read loop will clean up */ }
            }

            public void Close()
            {
                _closed = true;
                try { _tcp.Close(); } catch { }
            }
        }
    }
}
