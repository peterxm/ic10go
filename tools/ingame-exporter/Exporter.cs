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
            try
            {
                if (Prefab.AllPrefabs != null && Prefab.AllPrefabs.Count > 0)
                {
                    Scan();
                }
                else
                {
                    Debug.Log("[" + ModId + "] prefabs not loaded yet; waiting for OnPrefabsLoaded");
                    Prefab.OnPrefabsLoaded += Scan;
                }
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
                if (device != null && (device.Properties.Count > 0 || device.SlotProperties.Count > 0))
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

        private static DeviceEntry ScanDevice(Thing thing, ILogicable logicable, string name)
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

            properties.Sort((a, b) => a.LogicType.CompareTo(b.LogicType));
            slotProperties.Sort((a, b) => a.LogicSlotType.CompareTo(b.LogicSlotType));
            return new DeviceEntry(name, thing.PrefabHash, SafeTitle(thing, name), slots, properties, slotProperties);
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
            try { return thing.PrefabName ?? string.Empty; } catch { return string.Empty; }
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
            b.Append("  \"formatVersion\": 1,\n");
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
                b.Append("      \"slotProperties\": [");
                for (int j = 0; j < d.SlotProperties.Count; j++)
                {
                    var s = d.SlotProperties[j];
                    if (j > 0) b.Append(", ");
                    b.Append("{\"name\": ").Append(Json(s.Name))
                     .Append(", \"logicSlotType\": ").Append(s.LogicSlotType).Append("}");
                }
                b.Append("]\n");
                b.Append("    }");
                b.Append(i + 1 < devices.Count ? ",\n" : "\n");
            }
            b.Append("  ]\n}\n");
            return b.ToString();
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
            public readonly List<PropertyEntry> Properties;
            public readonly List<SlotPropertyEntry> SlotProperties;
            public DeviceEntry(string name, long hash, string title, int slotCount,
                               List<PropertyEntry> properties, List<SlotPropertyEntry> slotProperties)
            {
                Name = name; Hash = hash; Title = title; SlotCount = slotCount;
                Properties = properties; SlotProperties = slotProperties;
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
