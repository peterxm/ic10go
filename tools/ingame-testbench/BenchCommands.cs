// BenchCommands is the command table behind the NDJSON protocol. It runs on the
// Unity main thread (TestbenchPlugin.Update drains the queue), so it may touch
// the game freely.
//
// See docs/ingame-testbench.md for the protocol. Errors are BenchError, whose
// Code is a stable, non-localized identifier.

using System;
using System.Collections.Generic;
using Assets.Scripts;
using Newtonsoft.Json.Linq;

namespace Ic10Go.Testbench
{
    internal static class BenchCommands
    {
        private static JToken _selection; // {index|id|name}; null = first chip
        private static int _seq;

        public static bool Watching { get; private set; }
        public static int WatchIntervalMs { get; private set; } = 250;
        public static JToken WatchInclude { get; private set; }
        public static bool WatchAll { get; private set; }

        public static JObject Execute(string cmd, JObject args, TestbenchPlugin plugin)
        {
            if (args == null) args = new JObject();
            switch (cmd)
            {
                case "ping": return Ping();
                case "chip.list": return ChipList();
                case "chip.select": return ChipSelect(args);
                case "ports": return Ports(args);
                case "push": return Push(args);
                case "state": return State(args);
                case "set": return Set(args);
                case "get": return Get(args);
                case "run": return Run(args);
                case "reset": return Reset(args);
                case "pause": return PauseCmd(args, plugin);
                case "watch": return Watch(args);
                case "world.saves": return new JObject { ["saves"] = GameApi.Saves() };
                case "world.load": return WorldLoad(args);
                case "world.state": return WorldState();
                default: throw new BenchError("bad-request", "unknown command \"" + cmd + "\"");
            }
        }

        // -- basic ------------------------------------------------------------

        private static JObject Ping()
        {
            string gameVersion = "";
            try { gameVersion = GameManager.GetGameVersion() ?? ""; } catch { }
            bool paused = false;
            try { paused = WorldManager.IsGamePaused; } catch { }
            int chips = 0;
            try { chips = GameApi.ListChips().Count; } catch { }
            return new JObject
            {
                ["mod"] = TestbenchPlugin.ModId,
                ["version"] = TestbenchPlugin.ModVersion,
                ["gameVersion"] = gameVersion,
                ["paused"] = paused,
                ["chips"] = chips,
            };
        }

        private static JObject ChipList()
        {
            var arr = new JArray();
            foreach (var c in GameApi.ListChips()) arr.Add(c.ToJson());
            return new JObject { ["chips"] = arr };
        }

        private static JObject ChipSelect(JObject args)
        {
            _selection = args["chip"] ?? args["target"];
            var h = ResolveChip(null);
            return new JObject { ["chip"] = h.ToJson() };
        }

        private static JObject Ports(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            return GameApi.PortsReport(h);
        }

        private static JObject WorldLoad(JObject args)
        {
            string name = (string)(args["save"] ?? args["name"]);
            return new JObject { ["result"] = GameApi.LoadSave(name) ?? "" };
        }

        private static JObject WorldState()
        {
            return new JObject
            {
                ["state"] = GameApi.GameStateName(),
                ["world"] = GameApi.WorldName(),
                ["paused"] = GameApi.Paused(),
            };
        }

        // -- program ----------------------------------------------------------

        private static JObject Push(JObject args)
        {
            string code = (string)args["code"];
            if (string.IsNullOrEmpty(code)) throw new BenchError("bad-request", "push needs \"code\"");
            var loaders = new List<string>();
            var l = args["loaders"] as JArray;
            if (l != null) foreach (var t in l) loaders.Add((string)t);
            var h = ResolveChip(args["chip"]);
            return GameApi.Push(h, code, loaders);
        }

        private static JObject Reset(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            if (h.Chip == null) throw new BenchError("no-chip", "selected holder is not a ProgrammableChip");
            h.Chip.Reset();
            return new JObject { ["ok"] = true };
        }

        private static JObject Run(JObject args)
        {
            int ticks = args["ticks"] != null ? (int)args["ticks"] : 1;
            var h = ResolveChip(args["chip"]);
            return GameApi.Run(h, ticks);
        }

        private static JObject PauseCmd(JObject args, TestbenchPlugin plugin)
        {
            bool on = args["on"] == null || (bool)args["on"];
            GameApi.Pause(on);
            plugin.SetPausedByUs(on);
            return new JObject { ["paused"] = on };
        }

        // -- state ------------------------------------------------------------

        private static JObject State(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            bool all = args["all"] != null && (bool)args["all"];
            return GameApi.BuildState(h, args["include"], all);
        }

        private static JObject Set(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            bool force = args["force"] != null && (bool)args["force"];
            var writes = args["writes"] as JArray;
            if (writes == null) throw new BenchError("bad-request", "set needs \"writes\"");
            int applied = 0;
            foreach (var w in writes)
            {
                int port = ParsePort((string)w["port"]);
                var dev = GameApi.PortDevice(h.Holder, port);
                bool hasSlot = w["slot"] != null;
                GameApi.SetLogic(dev, (string)w["logic"], hasSlot ? (int)w["slot"] : 0, hasSlot, (double)w["value"], force);
                applied++;
            }
            return new JObject { ["applied"] = applied };
        }

        private static JObject Get(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            var reads = args["reads"] as JArray;
            if (reads == null) throw new BenchError("bad-request", "get needs \"reads\"");
            var values = new JArray();
            foreach (var r in reads)
            {
                int port = ParsePort((string)r["port"]);
                var dev = GameApi.PortDevice(h.Holder, port);
                bool hasSlot = r["slot"] != null;
                double v = GameApi.GetLogic(dev, (string)r["logic"], hasSlot ? (int)r["slot"] : 0, hasSlot);
                values.Add(new JObject
                {
                    ["port"] = PortLabel(port),
                    ["logic"] = (string)r["logic"],
                    ["value"] = GameApi.Num(v),
                });
            }
            return new JObject { ["values"] = values };
        }

        // -- watch ------------------------------------------------------------

        private static JObject Watch(JObject args)
        {
            bool on = args["on"] == null || (bool)args["on"];
            if (on)
            {
                ResolveChip(args["chip"]); // validate / select
                WatchIntervalMs = args["intervalMs"] != null ? Math.Max(50, (int)args["intervalMs"]) : 250;
                WatchInclude = args["include"];
                WatchAll = args["all"] != null && (bool)args["all"];
            }
            Watching = on;
            return new JObject { ["watching"] = on, ["intervalMs"] = WatchIntervalMs };
        }

        public static JObject BuildStateEvent(TestbenchPlugin plugin)
        {
            JObject ev = new JObject { ["event"] = "state", ["seq"] = ++_seq };
            try
            {
                var h = ResolveChip(null);
                ev["state"] = GameApi.BuildState(h, WatchInclude, WatchAll);
            }
            catch (BenchError e)
            {
                ev["state"] = JValue.CreateNull();
                ev["error"] = new JObject { ["code"] = e.Code, ["message"] = e.Message };
            }
            return ev;
        }

        // -- helpers ----------------------------------------------------------

        private static ChipHandle ResolveChip(JToken sel)
        {
            if (sel != null && sel.Type != JTokenType.Null) _selection = sel;
            return GameApi.Resolve(_selection);
        }

        private static string PortLabel(int port) => port < 0 ? "db" : "d" + port;

        private static int ParsePort(string port)
        {
            if (string.IsNullOrEmpty(port)) throw new BenchError("bad-request", "empty port");
            if (string.Equals(port, "db", StringComparison.OrdinalIgnoreCase)) return -1;
            string s = port[0] == 'd' || port[0] == 'D' ? port.Substring(1) : port;
            if (!int.TryParse(s, out int n) || n < 0 || n >= GameApi.Ports)
                throw new BenchError("bad-request", "bad port \"" + port + "\" (want db or d0..d" + (GameApi.Ports - 1) + ")");
            return n;
        }
    }
}
