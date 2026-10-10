// BenchCommands is the command table behind the NDJSON protocol. It runs on the
// Unity main thread (TestbenchPlugin.Update drains the queue), so it may touch
// the game freely.
//
// See docs/ingame-testbench.md for the protocol. Errors are BenchError, whose
// Code is a stable, non-localized identifier.

using System;
using System.Collections.Generic;
using Assets.Scripts;
using Assets.Scripts.Objects.Electrical;
using Assets.Scripts.Objects.Entities;
using Assets.Scripts.Objects.Motherboards;
using Assets.Scripts.Objects.Pipes;
using Newtonsoft.Json.Linq;
using UnityEngine;

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
                case "players": return Players();
                case "hud": return HudCmd(args);
                case "set": return Set(args);
                case "get": return Get(args);
                case "device": return Device(args);
                case "find": return Find(args);
                case "net": return Net(args);
                case "trace": return Trace(args);
                case "program": return ProgramCmd(args);
                case "writes": return Writes(args);
                case "run": return Run(args);
                case "reset": return Reset(args);
                case "pause": return PauseCmd(args, plugin);
                case "watch": return Watch(args);
                case "world.saves": return new JObject { ["saves"] = GameApi.Saves() };
                case "world.load": return WorldLoad(args);
                case "world.state": return WorldState();
                case "server": return GameApi.ServerInfo();
                case "serverlist": return new JObject { ["servers"] = GameApi.ServerList() };
                case "netdump": return GameApi.NetDump();
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

        private static JObject Players()
        {
            return new JObject { ["players"] = GameApi.PlayersReport() };
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
            bool actual = GameApi.Pause(on);
            plugin.SetPausedByUs(actual);
            return new JObject { ["paused"] = actual, ["requested"] = on };
        }

        // -- state ------------------------------------------------------------

        private static JObject State(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            bool all = args["all"] != null && (bool)args["all"];
            return GameApi.BuildState(h, args["include"], all);
        }

        /// <summary>hud {on?, clear?, target?:{x,y,z}|{chip}} drives the on-screen overlay.</summary>
        private static JObject HudCmd(JObject args)
        {
            if (args["on"] != null) Hud.Enabled = (bool)args["on"];
            if (args["offset"] != null) Hud.HeadingOffset = (float)(double)args["offset"];
            if (args["clear"] != null && (bool)args["clear"]) Hud.Clear();
            var t = args["target"];
            if (t != null && t.Type != JTokenType.Null)
            {
                if (t["x"] != null)
                {
                    var p = new Vector3((float)(double)t["x"], (float)(double)t["y"], (float)(double)t["z"]);
                    Hud.SetTarget(p, (string)t["label"]);
                }
                else if (t["chip"] != null)
                {
                    var h = GameApi.Resolve(t["chip"]);
                    Vector3 p;
                    if (!GameApi.TryChipPosition(h, out p))
                        throw new BenchError("no-chip", "tracked host has no world position");
                    Hud.SetTarget(p, string.IsNullOrEmpty(h.Name) ? h.Prefab : h.Name);
                }
                else if (t["player"] != null)
                {
                    string pname = (string)t["player"];
                    var found = GameApi.FindPlayerTarget(pname);
                    if (found == null) throw new BenchError("no-player", "\"" + pname + "\" is not in the world");
                    Hud.SetEntity(found, pname);
                }
                else
                {
                    throw new BenchError("bad-request", "hud target needs {x,y,z} or {chip} or {player}");
                }
            }
            return Hud.State();
        }

        private static JObject Set(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            bool force = args["force"] != null && (bool)args["force"];
            bool pulse = args["pulse"] != null && (bool)args["pulse"];
            var writes = args["writes"] as JArray;
            if (writes == null) throw new BenchError("bad-request", "set needs \"writes\"");
            int applied = 0;
            foreach (var w in writes)
            {
                bool byId = w["id"] != null;
                var dev = byId ? GameApi.DeviceById((int)(long)w["id"]) : ResolvePortOperand(h.Holder, (string)w["port"]);
                if (dev != null && w["conn"] != null) dev = GameApi.Connection(dev, (int)w["conn"]);
                if (dev == null)
                    throw new BenchError("no-device", byId ? ("no device with id " + w["id"]) : ("no device on port " + w["port"]));
                bool hasSlot = w["slot"] != null;
                GameApi.SetLogic(dev, (string)w["logic"], hasSlot ? (int)w["slot"] : 0, hasSlot, (double)w["value"], force, pulse);
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
                bool byId = r["id"] != null;
                var dev = byId ? GameApi.DeviceById((int)(long)r["id"]) : ResolvePortOperand(h.Holder, (string)r["port"]);
                if (dev != null && r["conn"] != null) dev = GameApi.Connection(dev, (int)r["conn"]);
                if (dev == null)
                    throw new BenchError("no-device", byId ? ("no device with id " + r["id"]) : ("no device on port " + r["port"]));
                bool hasSlot = r["slot"] != null;
                double v = GameApi.GetLogic(dev, (string)r["logic"], hasSlot ? (int)r["slot"] : 0, hasSlot);
                values.Add(new JObject
                {
                    ["port"] = byId ? ("id:" + r["id"]) : (string)r["port"],
                    ["logic"] = (string)r["logic"],
                    ["value"] = GameApi.Num(v),
                });
            }
            return new JObject { ["values"] = values };
        }

        /// <summary>device {ids:[...]} reads devices by ReferenceId (logic + slots),
        /// for scripts that address devices by id rather than through a port.</summary>
        private static JObject Device(JObject args)
        {
            var ids = args["ids"] as JArray;
            if (ids == null) throw new BenchError("bad-request", "device needs \"ids\"");
            var arr = new JArray();
            foreach (var t in ids)
            {
                int id = (int)(long)t;
                var e = GameApi.DescribeDeviceById(id);
                if (e == null) e = new JObject { ["id"] = id, ["present"] = false };
                arr.Add(e);
            }
            return new JObject { ["devices"] = arr };
        }

        /// <summary>find {prefab?, name?, max?} enumerates world devices (like IC10
        /// lb/lbn would match on the network) so devices not wired to a port can be
        /// inspected.</summary>
        private static JObject Find(JObject args)
        {
            string prefab = args["prefab"] != null ? args["prefab"].ToString() : null;
            string name = args["name"] != null ? args["name"].ToString() : null;
            int max = args["max"] != null ? (int)args["max"] : 0;
            return new JObject { ["devices"] = GameApi.FindDevices(prefab, name, max) };
        }

        /// <summary>net {chip?} lists the devices on the selected chip's data cable
        /// network (lb/lbn view), even if none are wired to a port.</summary>
        private static JObject Net(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            return new JObject { ["devices"] = GameApi.NetworkDevices(h.Holder) };
        }

        /// <summary>writes {clear?, from?} returns the device logic writes the
        /// program performed since the last clear (Harmony trace).</summary>
        private static JObject Writes(JObject args)
        {
            bool clear = args["clear"] != null && (bool)args["clear"];
            int from = args["from"] != null ? (int)args["from"] : 0;
            return WriteTrace.Snapshot(clear, from);
        }

        private static readonly string[] StoreOps = { "s", "sd", "ss", "put", "putd", "clr", "clrd", "sb", "sbn", "sbs" };

        /// <summary>trace {n} runs n instructions one at a time and returns every
        /// store instruction it executed (line + register snapshot), so a script
        /// that is believed to be idle can be checked.</summary>
        private static JObject Trace(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            if (h.Chip == null) throw new BenchError("no-chip", "selected holder is not a ProgrammableChip");
            int n = args["n"] != null ? (int)args["n"] : 512;
            if (n < 1) n = 1;
            if (n > 100000) n = 100000;
            string srcText = "";
            try { srcText = h.Chip.GetSourceCode(); } catch { }
            var src = srcText.Replace("\r", "").Split('\n');
            var hits = new JArray();
            int steps = 0;
            for (int i = 0; i < n; i++)
            {
                int pc = -1;
                try { pc = (int)h.Chip.LineNumber; } catch { }
                string text = pc >= 0 && pc < src.Length ? src[pc].Trim() : "";
                string op = text.Length == 0 ? "" : text.Split(' ')[0];
                if (Array.IndexOf(StoreOps, op) >= 0)
                {
                    var regs = GameApi.ReadRegisters(h.Chip);
                    var snap = new JArray();
                    if (regs != null) foreach (var v in regs) snap.Add(v);
                    hits.Add(new JObject { ["pc"] = pc, ["text"] = text, ["regs"] = snap });
                }
                try { h.Chip.Execute(1); } catch { break; }
                steps++;
            }
            return new JObject { ["steps"] = steps, ["hitCount"] = hits.Count, ["hits"] = hits };
        }

        /// <summary>program {} returns the chip's current IC10 source, so a chip
        /// can be decompiled/recompiled without guessing from a save.</summary>
        private static JObject ProgramCmd(JObject args)
        {
            var h = ResolveChip(args["chip"]);
            if (h.Chip == null) throw new BenchError("no-chip", "selected holder is not a ProgrammableChip");
            string code = "";
            try { code = h.Chip.GetSourceCode(); } catch { }
            int lines = code.Length == 0 ? 0 : code.Replace("\r", "").Split('\n').Length;
            return new JObject { ["lines"] = lines, ["code"] = code };
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
            try
            {
                return GameApi.Resolve(_selection);
            }
            catch (BenchError e) when (e.Code == "no-chip" && _selection != null)
            {
                // The pinned chip is gone (the world changed, or it despawned).
                // Forget it and fall back to the default so `state`/`push` keep
                // working right after loading a different save.
                _selection = null;
                return GameApi.Resolve(null);
            }
        }


        // ParsePortOperand parses an IC10 port operand: "db", "dN", and the
        // network forms "db:C" / "dN:C" (`:C` is the connection index — the
        // cable network a port's device exposes, so `d1:0 Channel0` is channel 0
        // of the network on that device's connection 0). Returns the device
        // index (int.MaxValue for db, -1 for db without a connection) and sets
        // `network` to the connection (int.MinValue when none was given).
        private static int ParsePortOperand(string port, out int network)
        {
            network = int.MinValue;
            if (string.IsNullOrEmpty(port)) throw new BenchError("bad-request", "empty port");
            string p = port;
            bool db = false;
            if (p.Length >= 2 && (p[0] == 'd' || p[0] == 'D') && (p[1] == 'b' || p[1] == 'B'))
            {
                db = true;
                p = p.Substring(2);
            }
            else if (p[0] == 'd' || p[0] == 'D')
            {
                p = p.Substring(1);
            }
            int colon = p.IndexOf(':');
            if (colon >= 0)
            {
                if (!int.TryParse(p.Substring(colon + 1), out network))
                    throw new BenchError("bad-request", "bad connection index in \"" + port + "\"");
                p = p.Substring(0, colon);
            }
            if (db)
                return network == int.MinValue ? -1 : int.MaxValue;
            if (!int.TryParse(p, out int n) || n < 0 || n >= GameApi.Ports)
                throw new BenchError("bad-request", "bad port \"" + port + "\" (want db, d0..d" + (GameApi.Ports - 1) + " or d<port>:<conn>)");
            return n;
        }

        // ResolvePortOperand turns a port operand into the logicable to read or
        // write: a device for "db"/"dN", or the cable network for "db:C"/"dN:C".
        private static ILogicable ResolvePortOperand(ICircuitHolder holder, string port)
        {
            int idx = ParsePortOperand(port, out int network);
            if (network == int.MinValue) return GameApi.PortDevice(holder, idx);
            return GameApi.Port(holder, idx, network);
        }
    }
}
