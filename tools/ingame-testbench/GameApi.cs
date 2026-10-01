// GameApi wraps the Stationeers types the testbench needs. Everything runs on
// the Unity main thread (BenchCommands is called from TestbenchPlugin.Update).
//
// Public API confirmed against Assembly-CSharp.dll (game 0.2.x):
//   ProgrammableChip.Execute(int runCount)          - one tick's worth of steps
//   ProgrammableChip.ReadMemory(int address)->double
//   ProgrammableChip.WriteMemory(int,double) / ClearMemory() / GetStackSize()
//   ProgrammableChip.SetSourceCode(string) / GetSourceCode() / Reset()
//   ProgrammableChip.LineNumber (double)
//   ProgrammableChipMotherboard.InputFinished(string)
//   ICircuitHolder.GetLogicableFromIndex(int deviceIndex, int networkIndex)
//   ILogicable.SetLogicValue(LogicType,double) / GetLogicValue(LogicType)
//   ILogicable.GetLogicValue(LogicSlotType,int slotId)
//   CircuitHolders.AllCircuitHolders (List<ICircuitHolder>)
//   WorldManager.IsGamePaused (static)
//
// Run `go run ./tools/dumpgameapi` after a game update to re-check them.

using System;
using System.Collections.Generic;
using System.IO;
using System.Reflection;
using System.Security.Cryptography;
using System.Text;
using Assets.Scripts;
using Assets.Scripts.Objects;
using Assets.Scripts.Objects.Electrical;
using Assets.Scripts.Objects.Entities;
using Assets.Scripts.Objects.Items;
using Assets.Scripts.Objects.Motherboards;
using Assets.Scripts.Objects.Pipes;
using Assets.Scripts.Serialization;
using Assets.Scripts.UI;
using Newtonsoft.Json.Linq;
using UnityEngine;

namespace Ic10Go.Testbench
{
    /// <summary>A protocol error that maps to a stable `error.code`.</summary>
    internal sealed class BenchError : Exception
    {
        public readonly string Code;
        public BenchError(string code, string message) : base(message) { Code = code; }
    }

    /// <summary>A chip that can be targeted by the protocol.</summary>
    internal sealed class ChipHandle
    {
        public int Index;
        public string Name;
        public string Prefab;
        public long ReferenceId;
        public ICircuitHolder Holder;
        public ProgrammableChip Chip; // null when the holder is not a ProgrammableChip

        public JObject ToJson()
        {
            var o = new JObject
            {
                ["id"] = ReferenceId,
                ["index"] = Index,
                ["name"] = Name,
            };
            if (!string.IsNullOrEmpty(Prefab)) o["prefab"] = Prefab;
            o["programmable"] = Chip != null;
            if (Chip != null)
            {
                try { o["chipPrefab"] = Chip.PrefabName; } catch { }
                try { o["line"] = Chip.LineNumber; } catch { }
                try { o["lines"] = GameApi.LineCount(GameApi.SourceOf(Chip)); } catch { }
                // Stable across restarts, unlike the ReferenceId: lets a client
                // match a local program to the in-game chip running it.
                try { o["fp"] = GameApi.Fingerprint(GameApi.SourceOf(Chip)); } catch { }
            }
            // World position of the host, so a client can tell you which one to
            // go look at. Present whenever the holder is a placed Thing.
            var loc = GameApi.LocationOf(Holder);
            if (loc != null) o["pos"] = loc;
            // Powered state: an unpowered host reports an empty program, which
            // otherwise looks like "can't download the code".
            if (Holder is Thing host)
            {
                try { o["powered"] = host.Powered; } catch { }
                try { o["power"] = Math.Round((double)host.PoweredValue, 2); } catch { }
            }
            return o;
        }
    }

    internal static class GameApi
    {
        private const BindingFlags Priv = BindingFlags.Instance | BindingFlags.NonPublic;

        private static readonly FieldInfo FRegs = typeof(ProgrammableChip).GetField("_Registers", Priv);
        private static readonly FieldInfo FStack = typeof(ProgrammableChip).GetField("_Stack", Priv);
        private static readonly FieldInfo FSp = typeof(ProgrammableChip).GetField("_StackPointerIndex", Priv);
        private static readonly FieldInfo FRa = typeof(ProgrammableChip).GetField("_ReturnAddressIndex", Priv);
        private static readonly FieldInfo FExec = typeof(ProgrammableChip).GetField("_executeIndex", Priv);
        private static readonly FieldInfo FCustomName = typeof(Thing).GetField("_customName", Priv);
        private static readonly Dictionary<Type, PropertyInfo[]> ChipPropCache = new Dictionary<Type, PropertyInfo[]>();

        /// <summary>
        /// Resolves the running ProgrammableChip behind a holder:
        ///  * the holder is itself a chip;
        ///  * a `ProgrammableChip` property (CircuitHousing);
        ///  * a chip slot — `ChipSlot` on suits, `_ProgrammableChipSlot` on
        ///    housings, or any `Slot` property whose occupant is a chip.
        /// </summary>
        private static ProgrammableChip ChipOf(ICircuitHolder h)
        {
            if (h is ProgrammableChip pc) return pc;
            var type = h.GetType();
            foreach (var prop in ChipProperties(type))
            {
                object value;
                try { value = prop.GetValue(h); } catch { continue; }
                if (value == null) continue;
                if (value is ProgrammableChip direct) return direct;
                var chip = ChipFromSlot(value);
                if (chip != null) return chip;
            }
            return null;
        }

        private static PropertyInfo[] ChipProperties(Type type)
        {
            PropertyInfo[] cached;
            if (ChipPropCache.TryGetValue(type, out cached)) return cached;

            const BindingFlags all = BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic;
            var list = new List<PropertyInfo>();
            var direct = type.GetProperty("ProgrammableChip", all);
            if (direct != null && direct.CanRead) list.Add(direct);
            foreach (var name in new[] { "ChipSlot", "_ProgrammableChipSlot" })
            {
                var p = type.GetProperty(name, all);
                if (p != null && p.CanRead && !list.Contains(p)) list.Add(p);
            }
            foreach (var p in type.GetProperties(BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic))
            {
                if (!p.CanRead || list.Contains(p)) continue;
                if (IsSlotType(p.PropertyType)) list.Add(p);
            }
            cached = list.ToArray();
            ChipPropCache[type] = cached;
            return cached;
        }

        private static bool IsSlotType(Type t)
        {
            return t != null && (t.FullName == "Assets.Scripts.Objects.Slot" || t.Name == "Slot");
        }

        private static ProgrammableChip ChipFromSlot(object slot)
        {
            if (slot == null) return null;
            var t = slot.GetType();

            // Prefer the Occupant property: Slot.Get() is ambiguous.
            try
            {
                var occ = t.GetProperty("Occupant", BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
                if (occ != null && occ.CanRead)
                {
                    var v = occ.GetValue(slot);
                    if (v is ProgrammableChip pc) return pc;
                }
            }
            catch { }

            // Fall back to a parameterless Get().
            try
            {
                foreach (var m in t.GetMethods(BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic))
                {
                    if (m.Name != "Get" || m.GetParameters().Length != 0 || m.ReturnType == typeof(void)) continue;
                    var v = m.Invoke(slot, null);
                    if (v is ProgrammableChip pc) return pc;
                }
            }
            catch { }
            return null;
        }

        public const int Ports = 6;

        // -- chips ------------------------------------------------------------

        public static List<ChipHandle> ListChips()
        {
            var result = new List<ChipHandle>();
            List<ICircuitHolder> holders;
            try { holders = CircuitHolders.AllCircuitHolders.ToList(); }
            catch (Exception ex) { throw new BenchError("internal", "AllCircuitHolders: " + ex.Message); }
            if (holders == null) return result;

            for (int i = 0; i < holders.Count; i++)
            {
                var h = holders[i];
                if (h == null) continue;
                var thing = h as Thing;
                result.Add(new ChipHandle
                {
                    Index = i,
                    Holder = h,
                    Chip = ChipOf(h),
                    Name = NameOf(thing, i),
                    Prefab = thing?.PrefabName,
                    ReferenceId = thing != null ? thing.ReferenceId : 0,
                });
            }
            return result;
        }

        private static string NameOf(Thing t, int index)
        {
            if (t != null)
            {
                try
                {
                    var custom = FCustomName?.GetValue(t) as string;
                    if (!string.IsNullOrEmpty(custom)) return custom;
                }
                catch { }
                if (!string.IsNullOrEmpty(t.name)) return t.name;
                if (!string.IsNullOrEmpty(t.PrefabName)) return t.PrefabName;
            }
            return "chip#" + index;
        }

        /// <summary>Resolves a `{id|name|index}` selector against the live chips.</summary>
        public static ChipHandle Resolve(JToken target)
        {
            var chips = ListChips();
            if (chips.Count == 0) throw new BenchError("no-chip", "no programmable chip is loaded; load the test-bench save first");

            if (target == null || target.Type == JTokenType.Null)
            {
                // Prefer a holder with a running chip; fall back to the first.
                foreach (var c in chips) if (c.Chip != null) return c;
                return chips[0];
            }

            if (target.Type != JTokenType.Object)
                throw new BenchError("bad-request", "chip selector must be an object like {\"name\": \"...\"}");

            if (target["index"] != null)
            {
                int i = (int)target["index"];
                if (i < 0 || i >= chips.Count) throw new BenchError("no-chip", "chip index " + i + " out of range");
                return chips[i];
            }
            if (target["id"] != null)
            {
                long id = (long)target["id"];
                foreach (var c in chips) if (c.ReferenceId == id) return c;
                throw new BenchError("no-chip", "no chip with ReferenceId " + id);
            }
            if (target["name"] != null)
            {
                string name = (string)target["name"];
                foreach (var c in chips)
                {
                    if (string.Equals(c.Name, name, StringComparison.OrdinalIgnoreCase)) return c;
                    if (string.Equals(c.Prefab, name, StringComparison.OrdinalIgnoreCase)) return c;
                }
                throw new BenchError("no-chip", "no chip named \"" + name + "\"");
            }
            throw new BenchError("bad-request", "chip target must have index, id or name");
        }

        // -- source / push ----------------------------------------------------

        public static string SourceOf(ProgrammableChip chip)
        {
            try { return chip.GetSourceCode(); } catch { return ""; }
        }

        public static int LineCount(string code)
        {
            if (string.IsNullOrEmpty(code)) return 0;
            int n = 1;
            foreach (char c in code) if (c == '\n') n++;
            return n;
        }

        /// <summary>
        /// The host's world position and yaw (degrees), or null when the holder is
        /// not a placed object. Reported by chip.list so a client can tell the user
        /// where a chip physically is.
        /// </summary>
        public static JObject LocationOf(ICircuitHolder holder)
        {
            var thing = holder as Thing;
            if (thing == null) return null;
            try
            {
                var t = thing.transform;
                if (t == null) return null;
                var p = t.position;
                if (float.IsNaN(p.x) || float.IsNaN(p.y) || float.IsNaN(p.z)) return null;
                if (float.IsInfinity(p.x) || float.IsInfinity(p.y) || float.IsInfinity(p.z)) return null;
                return new JObject
                {
                    ["x"] = Math.Round(p.x, 2),
                    ["y"] = Math.Round(p.y, 2),
                    ["z"] = Math.Round(p.z, 2),
                    ["yaw"] = Math.Round(t.eulerAngles.y, 1),
                };
            }
            catch { return null; }
        }

        /// <summary>
        /// A short hash of a program's normalized source (CRLF -> LF, trailing
        /// whitespace trimmed). The Go client computes the same value so a local
        /// file can be matched to the chip running it, independent of the
        /// ReferenceId (which changes on restart).
        /// </summary>
        public static string Fingerprint(string code)
        {
            if (string.IsNullOrEmpty(code)) return "";
            string norm = code.Replace("\r\n", "\n").Replace('\r', '\n').TrimEnd();
            using (var sha = SHA1.Create())
            {
                byte[] hash = sha.ComputeHash(Encoding.UTF8.GetBytes(norm));
                var sb = new StringBuilder(8);
                for (int i = 0; i < 4; i++) sb.Append(hash[i].ToString("x2"));
                return sb.ToString();
            }
        }

        /// <summary>All players (Human instances). Used by the HUD's live player
        /// tracking and the `players` command.</summary>
        public static List<Human> AllPlayers()
        {
            var list = new List<Human>();
            try
            {
                var all = Human.AllHumans;
                if (all != null) foreach (var h in all) if (h != null) list.Add(h);
            }
            catch { }
            return list;
        }

        /// <summary>The player's Steam name from their brain, or fallbacks.</summary>
        private static string BrainName(Brain b)
        {
            string name = "";
            try { name = b.SteamName ?? ""; } catch { }
            if (string.IsNullOrEmpty(name)) { try { name = b.TrackableName ?? ""; } catch { } }
            if (string.IsNullOrEmpty(name)) { try { name = b.name ?? ""; } catch { } }
            return name;
        }

        /// <summary>Every player we can find a body for, online and offline, from
        /// Brain.PlayerBrains. A disconnected player may still have a human in the
        /// world, and a dead one a body bag; both are reported with a position so
        /// the HUD can point at them. Falls back to living Humans.</summary>
        public static List<JObject> PlayerRows()
        {
            var rows = new List<JObject>();
            Human me = null;
            Vector3 mePos = Vector3.zero;
            try { me = Human.LocalHuman; if (me != null) mePos = me.transform.position; } catch { }

            var brains = (Dictionary<ulong, Brain>)null;
            try { brains = Brain.PlayerBrains; } catch { }
            if (brains != null)
            {
                foreach (var b in brains.Values)
                {
                    if (b == null) continue;
                    var o = new JObject { ["name"] = BrainName(b) };
                    bool online = false;
                    try { online = b.IsOnline; } catch { }
                    o["online"] = online;

                    Human h = null;
                    try { h = b.ParentHuman; } catch { }
                    DynamicBodyBag bag = (h == null) ? BodyBagOf(b) : null;
                    Thing t = h != null ? (Thing)h : (Thing)bag;

                    o["self"] = (h != null && h == me);
                    o["body"] = (bag != null);
                    o["trackable"] = (t != null);
                    if (t != null) FillPosition(o, t, me, mePos);
                    rows.Add(o);
                }
            }

            if (rows.Count == 0)
            {
                foreach (var h in AllPlayers())
                {
                    var o = new JObject { ["name"] = "", ["online"] = true, ["self"] = (h == me), ["trackable"] = true };
                    try { o["name"] = h.DisplayName ?? ""; } catch { }
                    FillPosition(o, h, me, mePos);
                    rows.Add(o);
                }
            }

            rows.Sort((a, b) =>
            {
                double ad = (double?)a["dist"] ?? double.MaxValue;
                double bd = (double?)b["dist"] ?? double.MaxValue;
                return ad.CompareTo(bd);
            });
            return rows;
        }

        /// <summary>Records pos/dist on a player row from a live entity's transform.</summary>
        private static void FillPosition(JObject o, Thing t, Human me, Vector3 mePos)
        {
            try
            {
                var p = t.transform.position;
                o["pos"] = new JObject
                {
                    ["x"] = Math.Round(p.x, 2),
                    ["y"] = Math.Round(p.y, 2),
                    ["z"] = Math.Round(p.z, 2),
                };
                if (me != null)
                {
                    float dx = p.x - mePos.x, dy = p.y - mePos.y, dz = p.z - mePos.z;
                    o["dist"] = Math.Round(Math.Sqrt(dx * dx + dy * dy + dz * dz), 1);
                }
            }
            catch { }
        }

        /// <summary>The body bag holding this brain, or null (the player is alive or
        /// gone). A body bag lets us still point at a dead player's remains.</summary>
        private static DynamicBodyBag BodyBagOf(Brain b)
        {
            foreach (var bag in AllBodyBags())
                if ((object)BagBrain(bag) == (object)b) return bag;

            // Fallback: the body bag's display name is normally part of the brain's
            // trackable name ("VAIDAM" -> "VAIDAM's Body Bag").
            string name = BrainName(b);
            if (!string.IsNullOrEmpty(name))
            {
                foreach (var bag in AllBodyBags())
                {
                    string dn = "";
                    try { dn = bag.PlayersDisplayName ?? ""; } catch { }
                    if (!string.IsNullOrEmpty(dn) &&
                        name.IndexOf(dn, StringComparison.OrdinalIgnoreCase) >= 0)
                        return bag;
                }
            }
            return null;
        }

        /// <summary>The brain inside a body bag. The game exposes this only as a
        /// private member, so read it by reflection.</summary>
        private static Brain BagBrain(DynamicBodyBag bag)
        {
            return Member(bag, "Brain") as Brain;
        }

        /// <summary>Reads a named property or field (public or not) from an object.</summary>
        private static object Member(object o, string name)
        {
            if (o == null) return null;
            var t = o.GetType();
            try
            {
                var p = t.GetProperty(name, BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
                if (p != null && p.CanRead) return p.GetValue(o, null);
            }
            catch { }
            try
            {
                var f = t.GetField(name, BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
                if (f != null) return f.GetValue(o);
            }
            catch { }
            return null;
        }

        /// <summary>Every body bag in the world (empty on error).</summary>
        private static List<DynamicBodyBag> AllBodyBags()
        {
            var list = new List<DynamicBodyBag>();
            try
            {
                var all = DynamicBodyBag.AllBodyBags;
                if (all != null) foreach (var bag in all) if (bag != null) list.Add(bag);
            }
            catch { }
            return list;
        }

        /// <summary>Players with position, distance from the local player and a
        /// self flag, nearest-first (offline players last).</summary>
        public static JArray PlayersReport()
        {
            var arr = new JArray();
            foreach (var o in PlayerRows()) arr.Add(o);
            return arr;
        }

        /// <summary>Resolves a player name to the live entity to track: their human
        /// (online, or a body still in the world) or their body bag when dead. Null
        /// when the name is unknown. Matches SteamName / trackable name, and human /
        /// body-bag display names, case-insensitive.</summary>
        public static Thing FindPlayerTarget(string name)
        {
            if (string.IsNullOrEmpty(name)) return null;
            try
            {
                var brains = Brain.PlayerBrains;
                if (brains != null)
                {
                    foreach (var b in brains.Values)
                    {
                        if (b == null) continue;
                        if (!string.Equals(BrainName(b), name, StringComparison.OrdinalIgnoreCase)) continue;
                        Human h = null;
                        try { h = b.ParentHuman; } catch { }
                        if (h != null) return h;
                        var bag = BodyBagOf(b);
                        if (bag != null) return bag;
                    }
                }
            }
            catch { }
            foreach (var bag in AllBodyBags())
            {
                string dn = "";
                try { dn = bag.PlayersDisplayName ?? ""; } catch { }
                if (string.Equals(dn, name, StringComparison.OrdinalIgnoreCase)) return bag;
            }
            foreach (var h in AllPlayers())
            {
                string dn = "";
                try { dn = h.DisplayName ?? ""; } catch { }
                if (string.Equals(dn, name, StringComparison.OrdinalIgnoreCase)) return h;
            }
            return null;
        }

        /// <summary>The world position of a chip host, for the HUD target.</summary>
        public static bool TryChipPosition(ChipHandle handle, out Vector3 pos)
        {
            pos = Vector3.zero;
            var thing = handle != null ? handle.Holder as Thing : null;
            if (thing == null) return false;
            try { pos = thing.transform.position; return true; }
            catch { return false; }
        }

        /// <summary>
        /// Uploads `code` (running each loader chunk once first, so data segments
        /// and hoisted setup writes land before the runtime starts).
        /// </summary>
        public static JObject Push(ChipHandle handle, string code, List<string> loaders)
        {
            var chip = handle.Chip;
            if (chip == null) throw new BenchError("no-chip", "selected holder is not a ProgrammableChip");

            int loaderOps = 0;
            if (loaders != null)
            {
                foreach (var loader in loaders)
                {
                    if (string.IsNullOrEmpty(loader)) continue;
                    chip.SetSourceCode(loader);
                    int ops = Math.Max(4096, LineCount(loader) * 16);
                    loaderOps += ops;
                    chip.Execute(ops); // bounded: loaders are straight-line stores
                }
            }

            chip.Reset();
            chip.SetSourceCode(code);

            var o = new JObject
            {
                ["chip"] = handle.ToJson(),
                ["lines"] = LineCount(code),
                ["loaderOps"] = loaderOps,
            };
            var err = ErrorOf(chip);
            if (err != null) o["compileError"] = err;
            return o;
        }

        // -- devices ----------------------------------------------------------

        public static ILogicable Port(ICircuitHolder holder, int index, int network)
        {
            if (holder == null) return null;
            try { return holder.GetLogicableFromIndex(index, network); }
            catch { return null; }
        }

        /// <summary>
        /// The port devices. `index < 0` means the host itself (`db` in IC10: the
        /// device the chip is mounted on). Otherwise CircuitHousing exposes a
        /// public ILogicable[] Devices array (index = dN); fall back to
        /// GetLogicableFromIndex.
        /// </summary>
        public static ILogicable PortDevice(ICircuitHolder holder, int index)
        {
            if (holder == null) return null;
            if (index < 0) return holder as ILogicable; // "db" = the host device
            // Suits/tablets bind ports directly to worn items (so
            // GetLogicableFromIndex yields a Thing); a CircuitHousing binds them
            // to cable networks, so its Devices[] array holds the real device.
            var dev = Port(holder, index, 0);
            if (dev is Thing) return dev;
            var arr = DevicesArray(holder);
            if (arr != null && index < arr.Length)
            {
                var d = arr.GetValue(index) as ILogicable;
                if (d != null) return d;
            }
            return dev;
        }

        /// <summary>Find a device by its ReferenceId, searching every circuit
        /// holder's cable network. Used to observe devices a script addresses by
        /// ReferenceId rather than through a port. Returns null when not found.</summary>
        public static ILogicable DeviceById(int id)
        {
            List<ICircuitHolder> holders;
            try { holders = CircuitHolders.AllCircuitHolders.ToList(); }
            catch { return null; }
            foreach (var h in holders)
            {
                if (h == null) continue;
                try
                {
                    var d = h.GetLogicableFromId(id);
                    if (d != null) return d;
                }
                catch { }
            }
            return null;
        }

        /// <summary>Describe a device by ReferenceId (logic + slots), or null when
        /// no device with that id is found on any circuit holder's network.</summary>
        public static JObject DescribeDeviceById(int id)
        {
            var dev = DeviceById(id);
            if (dev == null) return null;
            var e = DeviceEntry("id:" + id, dev, null);
            e["id"] = id;
            return e;
        }

        /// <summary>The host's port binding labels (db, d0..d5) from GetLogicBindings.</summary>
        private static string[] BindingLabels(ICircuitHolder holder)
        {
            try
            {
                var list = holder.GetLogicBindings();
                if (list == null || list.Count == 0) return null;
                var arr = new string[list.Count];
                for (int i = 0; i < list.Count; i++)
                    arr[i] = list[i] != null ? list[i].Label : null;
                return arr;
            }
            catch { return null; }
        }

        private static string BindingFor(string[] labels, int index)
        {
            if (labels == null) return null;
            int i = index < 0 ? 0 : index + 1; // 7 entries: db, d0..d5
            if (labels.Length == 6 && index >= 0) i = index; // 6 entries: d0..d5
            if (i < 0 || i >= labels.Length) return null;
            return labels[i];
        }

        private static readonly Dictionary<Type, FieldInfo> DevicesFields = new Dictionary<Type, FieldInfo>();

        private static Array DevicesArray(ICircuitHolder holder)
        {
            if (holder == null) return null;
            var type = holder.GetType();
            FieldInfo f;
            if (!DevicesFields.TryGetValue(type, out f))
            {
                f = type.GetField("Devices", BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
                DevicesFields[type] = f;
            }
            if (f == null) return null;
            try { return f.GetValue(holder) as Array; } catch { return null; }
        }

        /// <summary>Diagnostic dump of how ports/devices are wired for a holder.</summary>
        public static JObject PortsReport(ChipHandle handle)
        {
            var holder = handle.Holder;
            var o = new JObject { ["chip"] = handle.ToJson() };
            o["holder"] = DescribeHolder(holder);
            var labels = BindingLabels(holder);
            if (labels != null) o["bindings"] = new JArray(labels);
            o["devices"] = DescribeArray(DevicesArray(holder));
            o["deviceIds"] = RawArray(GetMember(holder, "_DeviceIDs"));
            o["deviceLabels"] = RawArray(GetMember(holder, "_DeviceLabels"));
            o["inputNetwork1"] = DescribeArray(GetMember(holder, "_inputNetwork1DevicesSorted") as Array);

            var lookups = new JArray();
            for (int d = 0; d < Ports; d++)
            {
                for (int n = 0; n < 2; n++)
                {
                    var dev = Port(holder, d, n);
                    if (dev == null) continue;
                    var e = DescribeLogicable(dev);
                    e["deviceIndex"] = d;
                    e["networkIndex"] = n;
                    lookups.Add(e);
                }
            }
            o["lookups"] = lookups;
            return o;
        }

        private static object GetMember(object o, string name)
        {
            if (o == null) return null;
            var t = o.GetType();
            var f = t.GetField(name, BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
            if (f != null) { try { return f.GetValue(o); } catch { } }
            var p = t.GetProperty(name, BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
            if (p != null) { try { return p.GetValue(o); } catch { } }
            return null;
        }

        private static JArray RawArray(object value)
        {
            var arr = value as Array;
            var outArr = new JArray();
            if (arr == null) return outArr;
            for (int i = 0; i < arr.Length; i++)
            {
                var v = arr.GetValue(i);
                outArr.Add(v == null ? JValue.CreateNull() : new JValue(v.ToString()));
            }
            return outArr;
        }

        private static JArray DescribeArray(Array arr)
        {
            var outArr = new JArray();
            if (arr == null) return outArr;
            for (int i = 0; i < arr.Length; i++)
            {
                var item = arr.GetValue(i);
                if (item == null) { outArr.Add(JValue.CreateNull()); continue; }
                outArr.Add(DescribeLogicable(item));
            }
            return outArr;
        }

        /// <summary>Diagnostic dump of the holder's chip-related properties.</summary>
        public static JObject DescribeHolder(object holder)
        {
            var o = new JObject { ["type"] = holder != null ? holder.GetType().FullName : null };
            var props = new JArray();
            if (holder != null)
            {
                foreach (var p in ChipProperties(holder.GetType()))
                {
                    var e = new JObject { ["name"] = p.Name, ["propType"] = p.PropertyType.Name };
                    try
                    {
                        var v = p.GetValue(holder);
                        if (v == null) { e["value"] = JValue.CreateNull(); }
                        else
                        {
                            e["valueType"] = v.GetType().FullName;
                            var thing = v as Thing;
                            if (thing != null) { e["prefab"] = thing.PrefabName; }
                            var g = v.GetType().GetMethod(
                                "Get", BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic,
                                null, Type.EmptyTypes, null);
                            if (g != null)
                            {
                                var r = g.Invoke(v, null);
                                if (r == null) { e["get"] = JValue.CreateNull(); }
                                else
                                {
                                    e["get"] = r.GetType().FullName;
                                    var rt = r as Thing;
                                    if (rt != null) { e["getPrefab"] = rt.PrefabName; }
                                }
                            }
                        }
                    }
                    catch (Exception ex) { e["error"] = ex.Message; }
                    props.Add(e);
                }
            }
            o["chipProps"] = props;
            return o;
        }

        private static JObject DescribeLogicable(object dev)
        {
            var o = new JObject { ["type"] = dev.GetType().Name };
            var thing = dev as Thing;
            if (thing != null)
            {
                o["prefab"] = thing.PrefabName;
                o["name"] = thing.name;
                o["id"] = thing.ReferenceId;
            }
            var lg = dev as ILogicable;
            if (lg != null)
            {
                try { o["hash"] = lg.GetPrefabHash(); } catch { }
                var vals = new JObject();
                foreach (var name in new[] { "Setting", "On", "Ratio", "Value", "Channel0", "StackSize" })
                {
                    try
                    {
                        var t = (LogicType)Enum.Parse(typeof(LogicType), name, true);
                        if (lg.CanLogicRead(t)) vals[name] = Num(lg.GetLogicValue(t));
                    }
                    catch { }
                }
                o["logic"] = vals;
            }
            var probe = ProbeProps(dev);
            if (probe != null) o["probe"] = probe;
            return o;
        }

        /// <summary>Reflection probe for a few device state flags (diagnostics).</summary>
        private static JObject ProbeProps(object dev)
        {
            var type = dev.GetType();
            if (!type.Name.Contains("Jetpack")) return null;
            var o = new JObject();
            foreach (var name in new[] { "JetPackActivate", "PropulsionActive", "IsThrusting", "HasPropellent", "PropellantDelta", "On" })
            {
                var p = type.GetProperty(name, BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
                if (p != null && p.CanRead)
                {
                    try { o[name] = (p.GetValue(dev) ?? "").ToString(); } catch { }
                }
            }
            return o;
        }

        public static LogicType ParseLogic(string name)
        {
            if (string.IsNullOrEmpty(name)) throw new BenchError("unknown-logic", "empty logic type");
            try { return (LogicType)Enum.Parse(typeof(LogicType), name, true); }
            catch { throw new BenchError("unknown-logic", "unknown logic type \"" + name + "\""); }
        }

        public static LogicSlotType ParseSlot(string name)
        {
            if (string.IsNullOrEmpty(name)) throw new BenchError("unknown-logic", "empty slot type");
            try { return (LogicSlotType)Enum.Parse(typeof(LogicSlotType), name, true); }
            catch { throw new BenchError("unknown-logic", "unknown slot type \"" + name + "\""); }
        }

        public static void SetLogic(ILogicable dev, string logic, int slot, bool hasSlot, double value, bool force = false, bool pulse = false)
        {
            if (dev == null) throw new BenchError("no-device", "no device on that port");
            if (hasSlot) throw new BenchError("bad-request", "slot writes are not supported (read-only)");
            var t = ParseLogic(logic);
            try
            {
                if (!force && !dev.CanLogicWrite(t)) throw new BenchError("unknown-logic", logic + " is not writable on this device (use force)");
                if (pulse) dev.SetLogicValue(t, 0.0); // rising edge for momentary logic
                dev.SetLogicValue(t, value);
            }
            catch (BenchError) { throw; }
            catch (Exception ex) { throw new BenchError("internal", "write " + logic + ": " + ex.Message); }
        }

        /// <summary>The chip's _Registers array (r0..r15, sp, ra), or null.</summary>
        public static double[] ReadRegisters(ProgrammableChip chip)
        {
            try { return FRegs?.GetValue(chip) as double[]; } catch { return null; }
        }

        public static double GetLogic(ILogicable dev, string logic, int slot, bool hasSlot)
        {
            if (dev == null) throw new BenchError("no-device", "no device on that port");
            try
            {
                var t = ParseLogic(logic);
                if (!hasSlot && !dev.CanLogicRead(t))
                    throw new BenchError("unknown-logic", logic + " is not readable on this device");
                if (hasSlot) return dev.GetLogicValue(ParseSlot(logic), slot);
                return dev.GetLogicValue(t);
            }
            catch (BenchError) { throw; }
            catch (Exception ex) { throw new BenchError("internal", "read " + logic + ": " + ex.Message); }
        }

        // -- state ------------------------------------------------------------

        public static JToken Num(double v)
        {
            if (double.IsNaN(v) || double.IsInfinity(v)) return JValue.CreateNull();
            return new JValue(v);
        }

        public static JObject BuildState(ChipHandle handle, JToken include, bool allStack)
        {
            var chip = handle.Chip;
            if (chip == null) throw new BenchError("no-chip", "selected holder is not a ProgrammableChip");
            var want = include ?? new JArray("registers", "stack", "devices", "program", "errors");
            bool Has(string s) => want.Type == JTokenType.Array && want.ToObject<List<string>>().Contains(s);

            var st = new JObject { ["chip"] = handle.ToJson() };
            st["paused"] = Paused();

            double[] regs = null;
            try { regs = FRegs?.GetValue(chip) as double[]; } catch { }
            int? spField = IntField(FSp, chip);
            int? raField = IntField(FRa, chip);
            int? pc = IntField(FExec, chip);

            if (Has("registers"))
            {
                var r = new JObject();
                if (regs != null)
                {
                    for (int i = 0; i < regs.Length; i++)
                    {
                        // _Registers is [r0..r15, sp, ra].
                        string key = i < 16 ? "r" + i : (i == 16 ? "sp" : i == 17 ? "ra" : "r" + i);
                        r[key] = Num(regs[i]);
                    }
                }
                if (r["ra"] == null && raField.HasValue) r["ra"] = raField.Value;
                if (r["sp"] == null && spField.HasValue) r["sp"] = spField.Value;
                st["registers"] = r;
            }

            if (Has("stack"))
            {
                int size = 0;
                try { size = chip.GetStackSize(); } catch { }
                if (size <= 0 && FStack != null) { try { size = ((double[])FStack.GetValue(chip))?.Length ?? 0; } catch { } }
                int spv = (regs != null && regs.Length > 16) ? (int)regs[16] : (spField ?? 0);
                int limit = allStack ? size : Math.Min(size, Math.Max(spv + 4, 16));
                double[] stack = null;
                try { stack = FStack?.GetValue(chip) as double[]; } catch { }
                var values = new JObject();
                for (int i = 0; i < limit; i++)
                {
                    double v;
                    if (stack != null && i < stack.Length) v = stack[i];
                    else { try { v = chip.ReadMemory(i); } catch { break; } }
                    values[i.ToString()] = Num(v);
                }
                st["stack"] = new JObject { ["size"] = size, ["sp"] = spv, ["values"] = values };
            }

            if (pc.HasValue) st["pc"] = pc.Value;
            try { st["line"] = chip.LineNumber; } catch { }

            if (Has("program")) st["program"] = new JObject { ["lines"] = LineCount(SourceOf(chip)) };

            if (Has("errors")) st["errors"] = ErrorOf(chip) ?? new JObject { ["code"] = "" };

            if (Has("devices"))
            {
                st["devices"] = BuildDevices(handle.Holder);
            }
            return st;
        }

        private static JArray BuildDevices(ICircuitHolder holder)
        {
            var list = new JArray();
            if (holder == null) return list;
            var labels = BindingLabels(holder);
            // `db` is the device the chip is mounted on (the host). On a device
            // host like an Air Conditioner this is how the program drives it.
            var host = holder as ILogicable;
            if (host != null) list.Add(DeviceEntry("db", host, BindingFor(labels, -1)));
            // Always emit every port (empty ones included) so the count matches
            // the host's bindings (d0..d5).
            for (int port = 0; port < Ports; port++)
            {
                ILogicable dev = null;
                try { dev = PortDevice(holder, port); }
                catch { }
                list.Add(DeviceEntry("d" + port, dev, BindingFor(labels, port)));
            }
            return list;
        }

        /// <summary>Describe a logicable device (logic values + slots) as JSON,
        /// the same shape BuildDevices uses. Returns null when dev is null.</summary>
        public static JObject DeviceEntry(string label, ILogicable dev, string binding)
        {
            var entry = new JObject { ["port"] = label };
            if (!string.IsNullOrEmpty(binding)) entry["binding"] = binding;

            if (dev == null)
            {
                entry["present"] = false;
                entry["logic"] = new JObject();
                return entry;
            }
            entry["present"] = true;

            var logic = new JObject();
            foreach (LogicType t in Enum.GetValues(typeof(LogicType)))
            {
                if ((int)t == 0) continue;
                try
                {
                    if (!dev.CanLogicRead(t)) continue;
                    logic[t.ToString()] = Num(dev.GetLogicValue(t));
                }
                catch { }
            }
            entry["logic"] = logic;

            // Logic slots (inventories): e.g. a suit has 8 slots with
            // Occupied / OccupantHash / Quantity / Class / PrefabHash / ...
            int totalSlots = 0;
            try { totalSlots = dev.TotalSlots; } catch { }
            if (totalSlots > 0)
            {
                var slots = new JArray();
                int n = Math.Min(totalSlots, 16);
                for (int i = 0; i < n; i++)
                {
                    var slot = new JObject { ["index"] = i, ["logic"] = new JObject() };
                    foreach (LogicSlotType st in Enum.GetValues(typeof(LogicSlotType)))
                    {
                        if ((int)st == 0) continue;
                        try
                        {
                            if (!dev.CanLogicRead(st, i)) continue;
                            slot["logic"][st.ToString()] = Num(dev.GetLogicValue(st, i));
                        }
                        catch { }
                    }
                    slots.Add(slot);
                }
                if (slots.Count > 0) entry["slots"] = slots;
            }

            var thing = dev as Thing;
            if (thing != null)
            {
                entry["prefab"] = thing.PrefabName;
                try
                {
                    var custom = FCustomName?.GetValue(thing) as string;
                    if (!string.IsNullOrEmpty(custom)) entry["name"] = custom;
                }
                catch { }
            }
            var probe = ProbeProps(dev);
            if (probe != null) entry["probe"] = probe;
            return entry;
        }

        private static JObject ErrorOf(ProgrammableChip chip)
        {
            try
            {
                string code = chip.GetErrorCode();
                bool compilation = false;
                try { compilation = chip.CompilationError; } catch { }
                string lineText = "";
                try { lineText = chip.ErrorLineNumberString; } catch { }
                int line = -1;
                if (!string.IsNullOrEmpty(lineText)) int.TryParse(lineText, out line);
                if (string.IsNullOrEmpty(code) && !compilation && line < 0) return null;
                return new JObject
                {
                    ["code"] = code ?? "",
                    ["compilation"] = compilation,
                    ["line"] = line,
                    ["lineText"] = lineText ?? "",
                };
            }
            catch { return null; }
        }

        // -- world / saves ----------------------------------------------------

        public static bool Paused()
        {
            try { return WorldManager.IsGamePaused; } catch { return false; }
        }

        public static string GameStateName()
        {
            try { return GameManager.GameState.ToString(); } catch { return ""; }
        }

        public static string WorldName()
        {
            try { return WorldManager.CurrentWorldName; } catch { return ""; }
        }

        /// <summary>Active Unity scene name (diagnostics).</summary>
        public static string SceneName()
        {
            try { return UnityEngine.SceneManagement.SceneManager.GetActiveScene().name; }
            catch { return ""; }
        }

        /// <summary>
        /// Loads a save file. This is the same entry point the main menu's
        /// "load latest" uses (LoadHelper.LoadGame(path, stationName)); the
        /// `loadgame` console command takes a world id, not a save, so it is
        /// not used here.
        /// </summary>
        public static string LoadSave(string name)
        {
            if (string.IsNullOrEmpty(name)) throw new BenchError("bad-request", "world.load needs a save name");
            string path = ResolveSavePath(name, out string station);
            try
            {
                LoadHelper.LoadGame(path, station);
                return "loading " + path;
            }
            catch (Exception ex)
            {
                throw new BenchError("internal", "LoadHelper.LoadGame: " + ex.Message);
            }
        }

        private static string SavesDir()
        {
            return Path.Combine(
                Environment.GetFolderPath(Environment.SpecialFolder.MyDocuments),
                "My Games", "Stationeers", "saves");
        }

        /// <summary>
        /// Resolves a save name (a folder under saves/) or a path to the actual
        /// .save file the game expects.
        /// </summary>
        public static string ResolveSavePath(string name, out string station)
        {
            station = name;
            if (File.Exists(name))
            {
                station = Path.GetFileNameWithoutExtension(name);
                return name;
            }
            string dir = SavesDir();
            string folder = Path.Combine(dir, name);
            if (Directory.Exists(folder))
            {
                station = Path.GetFileName(folder);
                string direct = Path.Combine(folder, station + ".save");
                if (File.Exists(direct)) return direct;
                string manual = NewestSave(Path.Combine(folder, "manualsave"));
                if (manual != null) return manual;
                string any = NewestSave(folder);
                if (any != null) return any;
            }
            string candidate = Path.Combine(dir, name);
            if (File.Exists(candidate)) { station = Path.GetFileNameWithoutExtension(candidate); return candidate; }
            if (File.Exists(candidate + ".save")) { station = name; return candidate + ".save"; }
            throw new BenchError("bad-request", "no save \"" + name + "\" under " + dir);
        }

        private static string NewestSave(string dir)
        {
            if (!Directory.Exists(dir)) return null;
            string best = null;
            DateTime bestTime = DateTime.MinValue;
            foreach (var f in Directory.GetFiles(dir, "*.save", SearchOption.AllDirectories))
            {
                var t = File.GetLastWriteTimeUtc(f);
                if (t > bestTime) { bestTime = t; best = f; }
            }
            return best;
        }

        /// <summary>Lists the save folders under My Games/Stationeers/saves.</summary>
        public static JArray Saves()
        {
            var arr = new JArray();
            try
            {
                string dir = SavesDir();
                if (Directory.Exists(dir))
                {
                    var dirs = Directory.GetDirectories(dir);
                    Array.Sort(dirs, StringComparer.OrdinalIgnoreCase);
                    foreach (var d in dirs) arr.Add(Path.GetFileName(d));
                }
            }
            catch { }
            return arr;
        }

        // -- pause ------------------------------------------------------------

        private static MethodInfo _pauseToggle;
        private static bool _pauseToggleLooked;

        /// <summary>
        /// Pauses / resumes the world. Uses the game's own pause toggle
        /// (InputSourceCode.PauseGameToggle), which also handles cursor /
        /// input state; setting WorldManager.IsGamePaused directly leaves the
        /// game frozen but not operable until a menu is toggled.
        /// </summary>
        public static void Pause(bool on)
        {
            if (TryGamePause(on)) return;
            try { WorldManager.SetGamePause(on); }
            catch (Exception ex) { throw new BenchError("internal", "pause: " + ex.Message); }
        }

        private static bool TryGamePause(bool on)
        {
            try
            {
                var inst = InputSourceCode.Instance;
                if (inst == null) return false;
                if (!_pauseToggleLooked)
                {
                    _pauseToggle = typeof(InputSourceCode).GetMethod(
                        "PauseGameToggle",
                        BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
                    _pauseToggleLooked = true;
                }
                if (_pauseToggle == null) return false;
                _pauseToggle.Invoke(inst, new object[] { on });
                return true;
            }
            catch { return false; }
        }

        /// <summary>Advances the chip by `ticks` game ticks (128 instructions each).</summary>
        public static JObject Run(ChipHandle handle, int ticks)
        {
            var chip = handle.Chip;
            if (chip == null) throw new BenchError("no-chip", "selected holder is not a ProgrammableChip");
            if (ticks < 0) ticks = 0;

            for (int i = 0; i < ticks; i++)
            {
                try { chip.Execute(128); }
                catch (Exception ex) { throw new BenchError("internal", "execute: " + ex.Message); }
            }
            var o = new JObject { ["ticks"] = ticks };
            try { o["line"] = chip.LineNumber; } catch { }
            var err = ErrorOf(chip);
            if (err != null) o["error"] = err;
            return o;
        }

        private static int? IntField(FieldInfo f, object o)
        {
            if (f == null) return null;
            try { return (int)f.GetValue(o); } catch { return null; }
        }
    }
}
