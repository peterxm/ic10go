// Device-write trace.
//
// The IC10 interpreter writes device logic through ILogicable.SetLogicValue.
// We Harmony-patch every implementation of it (resolved via the interface map,
// so explicit implementations are covered) and record each call, letting the
// harness compare the original and a recompiled program's write sequence even
// for write-only logic types (Setting/Color/Mode) that cannot be read back.
//
// This is the mod's only Harmony use.
using System;
using System.Collections.Generic;
using System.Reflection;
using Assets.Scripts.Objects;
using Assets.Scripts.Objects.Electrical;
using Assets.Scripts.Objects.Pipes;
using HarmonyLib;
using Newtonsoft.Json.Linq;

namespace Ic10Go.Testbench
{
    public static class WriteTrace
    {
        public struct Write
        {
            public long Seq;
            public long Id;
            public string Device;
            public string Logic;
            public int Slot; // -1 for a logic write, else the slot index
            public double Value;
        }

        private static readonly object Gate = new object();
        private static readonly List<Write> Log = new List<Write>();
        private static long _seq;

        private static void Record(object device, string logic, int slot, double value)
        {
            long id = 0;
            string name = null;
            try
            {
                if (device != null)
                {
                    var t = device.GetType();
                    var p = t.GetProperty("ReferenceId", BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
                    if (p != null && p.CanRead) id = Convert.ToInt64(p.GetValue(device));
                    var n = t.GetProperty("name", BindingFlags.Instance | BindingFlags.Public | BindingFlags.NonPublic);
                    if (n != null && n.CanRead) name = Convert.ToString(n.GetValue(device));
                }
            }
            catch { }
            lock (Gate)
            {
                _seq++;
                Log.Add(new Write { Seq = _seq, Id = id, Device = name, Logic = logic, Slot = slot, Value = value });
                if (Log.Count > 200000) Log.RemoveRange(0, 50000);
            }
        }

        private static void LogicPrefix(object __instance, object __0, double __1) => Record(__instance, __0?.ToString(), -1, __1);
        private static void SlotPrefix(object __instance, object __0, int __1, double __2) => Record(__instance, __0?.ToString(), __1, __2);

        /// <summary>Patch every ILogicable.SetLogicValue implementation.</summary>
        public static string Install()
        {
            var harmony = new HarmonyLib.Harmony("ic10go.testbench");
            var asm = typeof(ProgrammableChip).Assembly;
            var logicPrefix = new HarmonyMethod(typeof(WriteTrace).GetMethod(nameof(LogicPrefix), BindingFlags.Static | BindingFlags.NonPublic));
            var slotPrefix = new HarmonyMethod(typeof(WriteTrace).GetMethod(nameof(SlotPrefix), BindingFlags.Static | BindingFlags.NonPublic));
            var done = new HashSet<MethodBase>();
            int nLogic = 0, nSlot = 0, nTypes = 0;
            foreach (var t in asm.GetTypes())
            {
                if (!typeof(ILogicable).IsAssignableFrom(t)) continue;
                InterfaceMapping map;
                try { map = t.GetInterfaceMap(typeof(ILogicable)); }
                catch { continue; }
                nTypes++;
                for (int i = 0; i < map.InterfaceMethods.Length; i++)
                {
                    var im = map.InterfaceMethods[i];
                    if (im.Name != "SetLogicValue") continue;
                    var tm = map.TargetMethods[i];
                    if (tm == null || !done.Add(tm)) continue;
                    try
                    {
                        var ps = im.GetParameters();
                        if (ps.Length == 2)
                        {
                            harmony.Patch(tm, prefix: logicPrefix);
                            nLogic++;
                        }
                        else if (ps.Length == 3)
                        {
                            harmony.Patch(tm, prefix: slotPrefix);
                            nSlot++;
                        }
                    }
                    catch { }
                }
            }
            return $"ILogicable types={nTypes} logic={nLogic} slot={nSlot}";
        }

        /// <summary>Writes recorded so far (optionally from an index), and clears
        /// the log when clear is set.</summary>
        public static JObject Snapshot(bool clear, int from)
        {
            var arr = new JArray();
            lock (Gate)
            {
                for (int i = Math.Max(0, from); i < Log.Count; i++)
                {
                    var w = Log[i];
                    arr.Add(new JObject
                    {
                        ["seq"] = w.Seq,
                        ["id"] = w.Id,
                        ["device"] = w.Device,
                        ["logic"] = w.Logic,
                        ["slot"] = w.Slot,
                        ["value"] = w.Value,
                    });
                }
                int count = Log.Count;
                if (clear) Log.Clear();
                return new JObject { ["writes"] = arr, ["count"] = count };
            }
        }
    }
}
