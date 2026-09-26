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
using Assets.Scripts;
using Assets.Scripts.Objects;
using Assets.Scripts.Objects.Electrical;
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

        public static void SetLogic(ILogicable dev, string logic, int slot, bool hasSlot, double value, bool force = false)
        {
            if (dev == null) throw new BenchError("no-device", "no device on that port");
            if (hasSlot) throw new BenchError("bad-request", "slot writes are not supported (read-only)");
            var t = ParseLogic(logic);
            try
            {
                if (!force && !dev.CanLogicWrite(t)) throw new BenchError("unknown-logic", logic + " is not writable on this device (use force)");
                dev.SetLogicValue(t, value);
            }
            catch (BenchError) { throw; }
            catch (Exception ex) { throw new BenchError("internal", "write " + logic + ": " + ex.Message); }
        }

        public static double GetLogic(ILogicable dev, string logic, int slot, bool hasSlot)
        {
            if (dev == null) throw new BenchError("no-device", "no device on that port");
            try
            {
                if (hasSlot) return dev.GetLogicValue(ParseSlot(logic), slot);
                return dev.GetLogicValue(ParseLogic(logic));
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
                        string key = i < 16 ? "r" + i : (i == 16 ? "ra" : i == 17 ? "sp" : "r" + i);
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
                int spv = spField ?? 0;
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
            for (int port = 0; port < Ports; port++)
            {
                ILogicable dev;
                try { dev = PortDevice(holder, port); }
                catch { continue; }
                if (dev == null) continue;
                list.Add(DeviceEntry("d" + port, dev, BindingFor(labels, port)));
            }
            return list;
        }

        private static JObject DeviceEntry(string label, ILogicable dev, string binding)
        {
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
            var entry = new JObject
            {
                ["port"] = label,
                ["logic"] = logic,
            };
            if (!string.IsNullOrEmpty(binding)) entry["binding"] = binding;
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
