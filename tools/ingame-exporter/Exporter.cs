// ic10go in-game exporter.
//
// A minimal Stationeers mod that dumps what only the running game knows: every
// prefab's name / hash / display title, and which logic types each logic-capable
// device accepts for reading and writing. It writes JSON next to the game's
// user files; `tools/import-devices` turns that into ic10go tables.
//
// It has no Harmony patches and no runtime behaviour: it scans once, writes the
// files, and does nothing else. That keeps it far smaller than a mod that
// changes the game, and safe to leave installed.

using System;
using System.Collections.Generic;
using System.IO;
using System.Reflection;
using System.Text;
using Assets.Scripts;
using Assets.Scripts.Objects;
using Assets.Scripts.Objects.Electrical;
using Assets.Scripts.Objects.Motherboards;
using Assets.Scripts.Objects.Pipes;
using UnityEngine;

namespace Ic10Go.Exporter
{
    /// <summary>
    /// Entry point. StationeersLaunchPad instantiates the MonoBehaviour and
    /// calls OnLoaded; Awake is the fallback for any loader that only adds the
    /// component. OnLoaded must take a parameter or LaunchPad discards it.
    /// </summary>
    public sealed class ExporterPlugin : MonoBehaviour
    {
        public const string ModId = "ic10go-exporter";
        public const string ModName = "ic10go exporter";

        public void OnLoaded(List<Assembly> assemblies) => Initialize("OnLoaded (StationeersLaunchPad)");

        private void Awake() => Initialize("Awake");

        private static bool _initialized;

        private static void Initialize(string entryPoint)
        {
            if (_initialized) return;
            _initialized = true;
            Debug.Log("[" + ModId + "] " + ModName + " starting through " + entryPoint);
            // Prefabs arrive in stages: the game's own data, then each mod's (the
            // modular-console mod registers its parts from its entry point), and
            // some variants (the *Mirrored structures) later still. Scanning on the
            // first non-empty list -- or on Prefab.OnPrefabsLoaded, which fires
            // after the first stage -- misses the later ones: measured 1716 prefabs
            // and 547 devices that way, against 20 more devices in the previous
            // export. Wait until the count has stopped changing instead.
            _watchPrefabs = true;
        }

        private static bool _watchPrefabs;
        private static int _lastPrefabCount = -1;
        private static int _lastScannedCount = -1;
        private static int _scans;
        private static float _stableSince;

        private void Update()
        {
            if (!_watchPrefabs) return;
            int n = Prefab.AllPrefabs != null ? Prefab.AllPrefabs.Count : 0;
            float now = Time.realtimeSinceStartup;
            if (n == 0 || n != _lastPrefabCount)
            {
                _lastPrefabCount = n;
                _stableSince = now;
                return;
            }
            if (now - _stableSince < 5f) return;
            // Scan again when the count grew: loading a world registers more
            // prefabs (the *Mirrored structure variants among them), and a mod's
            // entry point may add its own after the first pass. Four scans is
            // plenty (boot, world load, and slack).
            if (n == _lastScannedCount || _scans >= 4) return;
            _lastScannedCount = n;
            _scans++;
            Debug.Log("[" + ModId + "] " + n + " prefabs stable for 5s; scanning (" + _scans + ")");
            try
            {
                Scan();
            }
            catch (Exception ex)
            {
                Debug.LogError("[" + ModId + "] failed: " + ex);
            }
        }

        private static void Scan()
        {
            var gameVersion = GameVersion();
            var prefabs = new List<PrefabEntry>();
            var devices = new List<DeviceEntry>();

            foreach (var thing in Prefab.AllPrefabs)
            {
                if (thing == null) continue;

                string name = SafeName(thing);
                if (string.IsNullOrEmpty(name))
                    continue;

                prefabs.Add(new PrefabEntry(name, thing.PrefabHash, SafeTitle(thing, name)));

                if (!(thing is ILogicable logicable))
                    continue;

                var device = ScanDevice(thing, logicable, name);
                if (device != null && (device.Properties.Count > 0 || device.SlotProperties.Count > 0 || device.Slots.Count > 0))
                    devices.Add(device);
            }

            prefabs.Sort((a, b) => string.CompareOrdinal(a.Name, b.Name));
            devices.Sort((a, b) => string.CompareOrdinal(a.Name, b.Name));

            string folder = OutputFolder();
            Directory.CreateDirectory(folder);
            File.WriteAllText(Path.Combine(folder, "prefabs.json"), WritePrefabsJson(gameVersion, prefabs));
            File.WriteAllText(Path.Combine(folder, "devices.json"), WriteDevicesJson(gameVersion, devices));

            Debug.Log("[" + ModId + "] wrote " + prefabs.Count + " prefabs and " + devices.Count +
                      " logic devices to " + folder);
        }

        private static DeviceEntry? ScanDevice(Thing thing, ILogicable logicable, string name)
        {
            var properties = new List<PropertyEntry>();

            foreach (LogicType type in Enum.GetValues(typeof(LogicType)))
            {
                int value = (int)type;
                if (value == 0) continue; // None

                bool read = SafeCanRead(logicable, type);
                bool write = SafeCanWrite(logicable, type);
                if (read || write)
                    properties.Add(new PropertyEntry(type.ToString(), value, read, write));
            }

            int slots = SafeSlotCount(logicable);
            var slotProperties = new List<SlotPropertyEntry>();
            if (slots > 0)
            {
                foreach (LogicSlotType type in Enum.GetValues(typeof(LogicSlotType)))
                {
                    int value = (int)type;
                    if (value == 0) continue; // None
                    if (SafeCanReadSlot(logicable, type, 0))
                        slotProperties.Add(new SlotPropertyEntry(type.ToString(), value));
                }
            }

            var slotWriteable = logicable as ISlotWriteable;
            var slotEntries = new List<SlotEntry>();
            for (int i = 0; i < slots; i++)
                slotEntries.Add(ScanSlot(logicable, slotWriteable, i));

            properties.Sort((a, b) => a.LogicType.CompareTo(b.LogicType));
            slotProperties.Sort((a, b) => a.LogicSlotType.CompareTo(b.LogicSlotType));
            return new DeviceEntry(name, thing.PrefabHash, SafeTitle(thing, name), slots,
                                   slotWriteable != null, properties, slotProperties, slotEntries);
        }

        /// <summary>
        /// One prefab slot: what the slot accepts (class / specific prefabs) and
        /// which slot logic types `ls`/`ss` accept for it. The game answers these
        /// per slot, not per device -- a suit storage's helmet slot understands
        /// On/Lock/Open while its suit slot does not, and only the suit slot adds
        /// PressureWaste/PressureAir.
        /// </summary>
        private static SlotEntry ScanSlot(ILogicable logicable, ISlotWriteable? slotWriteable, int index)
        {
            Slot? slot = SafeGetSlot(logicable, index);
            string cls = slot != null ? slot.Type.ToString() : "";
            string key = slot?.StringKey ?? "";
            int hash = slot?.StringHash ?? 0;
            bool interactable = slot != null && slot.IsInteractable;
            bool swappable = slot != null && slot.IsSwappable;
            bool locked = slot != null && slot.IsLocked;
            long[] specific = Array.Empty<long>();
            if (slot?.SpecificTypePrefabHashes != null)
            {
                specific = new long[slot.SpecificTypePrefabHashes.Length];
                for (int i = 0; i < specific.Length; i++)
                    specific[i] = slot.SpecificTypePrefabHashes[i];
            }

            var read = new List<SlotPropertyEntry>();
            var write = new List<SlotPropertyEntry>();
            bool isConsole = logicable is Assets.Scripts.Objects.Electrical.Console;
            foreach (LogicSlotType type in Enum.GetValues(typeof(LogicSlotType)))
            {
                int value = (int)type;
                if (value == 0) continue; // None
                if (SafeCanReadSlot(logicable, type, index))
                    read.Add(new SlotPropertyEntry(type.ToString(), value));
                if (slotWriteable != null && SafeSlotWritable(slotWriteable, type, index, cls, isConsole))
                    write.Add(new SlotPropertyEntry(type.ToString(), value));
            }
            read.Sort((a, b) => a.LogicSlotType.CompareTo(b.LogicSlotType));
            write.Sort((a, b) => a.LogicSlotType.CompareTo(b.LogicSlotType));
            return new SlotEntry(index, cls, key, hash, specific, interactable, swappable, locked, read, write);
        }

        /// <summary>
        /// Whether `ss` accepts a slot logic type. The virtual CanLogicWrite is
        /// tried first; Device.CanLogicWrite refuses on an unfinished build, and a
        /// prefab template is never "completed", so fall back to the same
        /// class-based rules it applies once built (Device.cs; Console adds Mode).
        /// Hard-coding them here is deliberate: the scan has no built instance.
        /// </summary>
        private static bool SafeSlotWritable(ISlotWriteable l, LogicSlotType type, int slot, string cls, bool isConsole)
        {
            try { if (l.CanLogicWrite(type, slot)) return true; } catch { }
            switch (type)
            {
                case LogicSlotType.On:
                    return cls == "Helmet" || cls == "Tool" || cls == "Appliance";
                case LogicSlotType.Lock:
                    return cls == "Helmet";
                case LogicSlotType.Open:
                    return cls == "Helmet" || cls == "GasCanister" || cls == "LiquidCanister" || cls == "LiquidBottle";
                case LogicSlotType.Mode:
                    return isConsole;
                default:
                    return false;
            }
        }

        private static Slot? SafeGetSlot(ILogicable l, int index)
        {
            try { return l.GetSlot(index); } catch { return null; }
        }

        // -- Defensive probing ------------------------------------------------
        // CanLogicRead/Write are virtual and implemented by hundreds of classes;
        // some assume world state a loose prefab does not have and can throw. An
        // exception means "not supported".

        private static bool SafeCanRead(ILogicable l, LogicType t)
        {
            try { return l.CanLogicRead(t); } catch { return false; }
        }

        private static bool SafeCanWrite(ILogicable l, LogicType t)
        {
            try { return l.CanLogicWrite(t); } catch { return false; }
        }

        private static bool SafeCanReadSlot(ILogicable l, LogicSlotType t, int slot)
        {
            try { return l.CanLogicRead(t, slot); } catch { return false; }
        }

        private static int SafeSlotCount(ILogicable l)
        {
            try { return Math.Max(0, l.TotalSlots); } catch { return 0; }
        }

        private static string SafeName(Thing thing)
        {
            try
            {
                string name = thing.PrefabName;
                if (!string.IsNullOrEmpty(name)) return name;
            }
            catch { }
            // Prefabs can leave PrefabName empty; the asset's own name is what a
            // hash("...") in a program has to match. This is not only a mod
            // problem: LettersMod ships Text_0..Text_z (and LText_*) with an empty
            // PrefabName, and the game reports CRC32("Text_K") = 1448182275 for
            // that character, so the asset name is the hash key. T.U.B.E. has the
            // same shape for its rounded wall/panel variants. Being untitled (the
            // game shows "<N:CN:...>" for them) is not a reason to drop them, and
            // it is not a claim that they are the game's own prefabs -- the
            // exporter writes whatever the loaded mod set provides.
            try { return thing.name ?? string.Empty; } catch { return string.Empty; }
        }

        private static string SafeTitle(Thing thing, string fallback)
        {
            try
            {
                string title = thing.DisplayName;
                return string.IsNullOrEmpty(title) ? fallback : title;
            }
            catch { return fallback; }
        }

        private static string GameVersion()
        {
            try { return GameManager.GetGameVersion() ?? string.Empty; } catch { return string.Empty; }
        }

        // -- Output -----------------------------------------------------------

        /// <summary>My Games/Stationeers/ic10go, the same user folder mods use.</summary>
        public static string OutputFolder()
        {
            string docs = Environment.GetFolderPath(Environment.SpecialFolder.MyDocuments);
            return Path.Combine(docs, "My Games", "Stationeers", "ic10go");
        }

        private static string WritePrefabsJson(string version, List<PrefabEntry> prefabs)
        {
            var b = new StringBuilder();
            b.Append("{\n");
            b.Append("  \"formatVersion\": 1,\n");
            b.Append("  \"gameVersion\": ").Append(Json(version)).Append(",\n");
            b.Append("  \"prefabCount\": ").Append(prefabs.Count).Append(",\n");
            b.Append("  \"prefabs\": [\n");
            for (int i = 0; i < prefabs.Count; i++)
            {
                var p = prefabs[i];
                b.Append("    {\"name\": ").Append(Json(p.Name))
                 .Append(", \"hash\": ").Append(p.Hash.ToString())
                 .Append(", \"displayName\": ").Append(Json(p.Title)).Append("}");
                b.Append(i + 1 < prefabs.Count ? ",\n" : "\n");
            }
            b.Append("  ]\n}\n");
            return b.ToString();
        }

        private static string WriteDevicesJson(string version, List<DeviceEntry> devices)
        {
            var b = new StringBuilder();
            b.Append("{\n");
            b.Append("  \"formatVersion\": 2,\n");
            b.Append("  \"gameVersion\": ").Append(Json(version)).Append(",\n");
            b.Append("  \"deviceCount\": ").Append(devices.Count).Append(",\n");
            b.Append("  \"devices\": [\n");
            for (int i = 0; i < devices.Count; i++)
            {
                var d = devices[i];
                b.Append("    {\n");
                b.Append("      \"prefabName\": ").Append(Json(d.Name)).Append(",\n");
                b.Append("      \"prefabHash\": ").Append(d.Hash.ToString()).Append(",\n");
                b.Append("      \"displayName\": ").Append(Json(d.Title)).Append(",\n");
                b.Append("      \"slotCount\": ").Append(d.SlotCount).Append(",\n");
                b.Append("      \"slotWriteable\": ").Append(d.SlotWriteable ? "true" : "false").Append(",\n");
                b.Append("      \"properties\": [");
                for (int j = 0; j < d.Properties.Count; j++)
                {
                    var p = d.Properties[j];
                    if (j > 0) b.Append(", ");
                    b.Append("{\"name\": ").Append(Json(p.Name))
                     .Append(", \"logicType\": ").Append(p.LogicType)
                     .Append(", \"read\": ").Append(p.Read ? "true" : "false")
                     .Append(", \"write\": ").Append(p.Write ? "true" : "false").Append("}");
                }
                b.Append("],\n");
                // Slot 0's readable set, kept at the top level for consumers of
                // format 1 (and for IZCode, whose catalog has the same shape).
                b.Append("      \"slotProperties\": [");
                WriteSlotProperties(b, d.SlotProperties);
                b.Append("],\n");
                b.Append("      \"slots\": [");
                for (int j = 0; j < d.Slots.Count; j++)
                {
                    var s = d.Slots[j];
                    if (j > 0) b.Append(", ");
                    b.Append("{\"index\": ").Append(s.Index)
                     .Append(", \"class\": ").Append(Json(s.Class))
                     .Append(", \"key\": ").Append(Json(s.Key))
                     .Append(", \"keyHash\": ").Append(s.KeyHash)
                     .Append(", \"interactable\": ").Append(s.Interactable ? "true" : "false")
                     .Append(", \"swappable\": ").Append(s.Swappable ? "true" : "false")
                     .Append(", \"locked\": ").Append(s.Locked ? "true" : "false")
                     .Append(", \"specificPrefabHashes\": [");
                    for (int k = 0; k < s.SpecificPrefabHashes.Length; k++)
                    {
                        if (k > 0) b.Append(", ");
                        b.Append(s.SpecificPrefabHashes[k].ToString());
                    }
                    b.Append("], \"read\": [");
                    WriteSlotProperties(b, s.Read);
                    b.Append("], \"write\": [");
                    WriteSlotProperties(b, s.Write);
                    b.Append("]}");
                }
                b.Append("]\n");
                b.Append("    }");
                b.Append(i + 1 < devices.Count ? ",\n" : "\n");
            }
            b.Append("  ]\n}\n");
            return b.ToString();
        }

        private static void WriteSlotProperties(StringBuilder b, List<SlotPropertyEntry> list)
        {
            for (int j = 0; j < list.Count; j++)
            {
                var s = list[j];
                if (j > 0) b.Append(", ");
                b.Append("{\"name\": ").Append(Json(s.Name))
                 .Append(", \"logicSlotType\": ").Append(s.LogicSlotType).Append("}");
            }
        }

        private static string Json(string s)
        {
            if (string.IsNullOrEmpty(s)) return "\"\"";
            var b = new StringBuilder(s.Length + 2);
            b.Append('"');
            foreach (char c in s)
            {
                switch (c)
                {
                    case '"': b.Append("\\\""); break;
                    case '\\': b.Append("\\\\"); break;
                    case '\n': b.Append("\\n"); break;
                    case '\r': b.Append("\\r"); break;
                    case '\t': b.Append("\\t"); break;
                    default:
                        if (c < 0x20) b.Append("\\u").Append(((int)c).ToString("x4"));
                        else b.Append(c);
                        break;
                }
            }
            b.Append('"');
            return b.ToString();
        }

        private sealed class PrefabEntry
        {
            public readonly string Name;
            public readonly long Hash;
            public readonly string Title;
            public PrefabEntry(string name, long hash, string title) { Name = name; Hash = hash; Title = title; }
        }

        private sealed class DeviceEntry
        {
            public readonly string Name;
            public readonly long Hash;
            public readonly string Title;
            public readonly int SlotCount;
            public readonly bool SlotWriteable;
            public readonly List<PropertyEntry> Properties;
            public readonly List<SlotPropertyEntry> SlotProperties;
            public readonly List<SlotEntry> Slots;
            public DeviceEntry(string name, long hash, string title, int slotCount, bool slotWriteable,
                               List<PropertyEntry> properties, List<SlotPropertyEntry> slotProperties,
                               List<SlotEntry> slots)
            {
                Name = name; Hash = hash; Title = title; SlotCount = slotCount; SlotWriteable = slotWriteable;
                Properties = properties; SlotProperties = slotProperties; Slots = slots;
            }
        }

        private sealed class SlotEntry
        {
            public readonly int Index;
            public readonly string Class;
            public readonly string Key;
            public readonly int KeyHash;
            public readonly long[] SpecificPrefabHashes;
            public readonly bool Interactable;
            public readonly bool Swappable;
            public readonly bool Locked;
            public readonly List<SlotPropertyEntry> Read;
            public readonly List<SlotPropertyEntry> Write;
            public SlotEntry(int index, string cls, string key, int keyHash, long[] specificPrefabHashes,
                             bool interactable, bool swappable, bool locked,
                             List<SlotPropertyEntry> read, List<SlotPropertyEntry> write)
            {
                Index = index; Class = cls; Key = key; KeyHash = keyHash;
                SpecificPrefabHashes = specificPrefabHashes;
                Interactable = interactable; Swappable = swappable; Locked = locked;
                Read = read; Write = write;
            }
        }

        private sealed class PropertyEntry
        {
            public readonly string Name;
            public readonly int LogicType;
            public readonly bool Read;
            public readonly bool Write;
            public PropertyEntry(string name, int logicType, bool read, bool write)
            { Name = name; LogicType = logicType; Read = read; Write = write; }
        }

        private sealed class SlotPropertyEntry
        {
            public readonly string Name;
            public readonly int LogicSlotType;
            public SlotPropertyEntry(string name, int logicSlotType) { Name = name; LogicSlotType = logicSlotType; }
        }
    }
}
