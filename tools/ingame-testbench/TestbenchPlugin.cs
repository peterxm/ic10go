// ic10go in-game testbench.
//
// A tiny local server for IC10 development. The ic10go toolchain
// (`ic10c testbench`) and the VSCode extension connect over NDJSON/TCP and ask
// the running game to:
//
//   * upload a compiled program to a chip,
//   * set / read device inputs and outputs,
//   * step the chip deterministically while the world is paused,
//   * read back registers, stack, program counter and device values.
//
// It has no Harmony patches: everything goes through the game's public API
// (ProgrammableChip.Execute / ReadMemory / SetSourceCode, ILogicable.SetLogicValue,
// CircuitHolders.AllCircuitHolders), with reflection only for the chip's private
// register/stack arrays. All game access happens on the Unity main thread.
//
// It is deliberately opt-in: it binds to 127.0.0.1 and does nothing until a
// client connects. A fixed test-bench save (d0 LED, d1 Logic Dial, d2 spare)
// is all the setup it needs.

using System;
using System.Collections.Generic;
using System.IO;
using System.Reflection;
using Newtonsoft.Json.Linq;
using UnityEngine;

namespace Ic10Go.Testbench
{
    /// <summary>
    /// Entry point. StationeersLaunchPad instantiates the MonoBehaviour and
    /// calls OnLoaded; Awake is the fallback for any loader that only adds the
    /// component. OnLoaded must take a parameter or LaunchPad discards it.
    /// </summary>
    public sealed class TestbenchPlugin : MonoBehaviour
    {
        public const string ModId = "ic10go-testbench";
        public const string ModName = "ic10go testbench";
        public const string ModVersion = "0.1.0";

        private static bool _initialized;

        private readonly Queue<Action> _main = new Queue<Action>();
        private BenchServer _server;
        private TestbenchConfig _config;
        private float _watchNext;
        private bool _pausedByUs;
        private bool _autoloadDone;
        private float _autoloadNext = 3f;

        public void OnLoaded(List<Assembly> assemblies) => Initialize("OnLoaded (StationeersLaunchPad)");

        private void Awake() => Initialize("Awake");

        private void Initialize(string entryPoint)
        {
            if (_initialized) return;
            _initialized = true;
            Debug.Log("[" + ModId + "] " + ModName + " starting through " + entryPoint);
            try
            {
                DontDestroyOnLoad(gameObject);
                var cfg = TestbenchConfig.Load();
                _config = cfg;
                _autoloadNext = Mathf.Max(5f, cfg.AutoloadDelay);
                _server = new BenchServer(cfg.Host, cfg.Port, this);
                _server.Start();
                Debug.Log("[" + ModId + "] listening on " + cfg.Host + ":" + cfg.Port);
                if (!string.IsNullOrEmpty(cfg.Autoload))
                    Debug.Log("[" + ModId + "] will autoload save \"" + cfg.Autoload + "\" at the main menu");
            }
            catch (Exception ex)
            {
                Debug.LogError("[" + ModId + "] failed to start: " + ex);
            }
        }

        /// <summary>Runs action on the Unity main thread (next Update).</summary>
        public void RunOnMain(Action action)
        {
            lock (_main) _main.Enqueue(action);
        }

        /// <summary>Main-thread execution of a command; used by BenchServer.</summary>
        public JObject Dispatch(string cmd, JObject args)
        {
            return BenchCommands.Execute(cmd, args, this);
        }

        internal void SetPausedByUs(bool value) => _pausedByUs = value;

        internal bool PausedByUs => _pausedByUs;

        private void Update()
        {
            // Game work only ever happens here.
            while (true)
            {
                Action next;
                lock (_main)
                {
                    if (_main.Count == 0) break;
                    next = _main.Dequeue();
                }
                try { next(); }
                catch (Exception ex) { Debug.LogError("[" + ModId + "] main-thread action failed: " + ex); }
            }

            // Live state push for `watch`.
            if (_server != null && BenchCommands.Watching)
            {
                float interval = BenchCommands.WatchIntervalMs / 1000f;
                if (Time.realtimeSinceStartup >= _watchNext)
                {
                    _watchNext = Time.realtimeSinceStartup + Mathf.Max(0.05f, interval);
                    try { _server.Broadcast(BenchCommands.BuildStateEvent(this)); }
                    catch (Exception ex) { Debug.LogError("[" + ModId + "] watch failed: " + ex); }
                }
            }

            // Optional autoload of a configured save. Loading during
            // GameManager.Start crashes the game, so wait until the main menu
            // scene (Base) is actually up, then load exactly once.
            if (_config != null && !string.IsNullOrEmpty(_config.Autoload) && !_autoloadDone)
            {
                if (Time.realtimeSinceStartup >= _autoloadNext)
                {
                    string gs = GameApi.GameStateName();
                    string scene = GameApi.SceneName();
                    if (gs == "Running" || gs == "Joining" || gs == "Waiting" || gs == "Paused" || gs == "Loading")
                    {
                        _autoloadDone = true;
                        Debug.Log("[" + ModId + "] autoload: world active (" + gs + "), done");
                    }
                    else if (gs == "None")
                    {
                        bool menuUp = scene == "Base" || (Time.realtimeSinceStartup > 180f && scene != "" && scene != "Splash");
                        if (menuUp)
                        {
                            _autoloadDone = true;
                            Debug.Log("[" + ModId + "] autoload (scene=" + scene + " t=" + Time.realtimeSinceStartup.ToString("0") + "): " + _config.Autoload);
                            try { Debug.Log("[" + ModId + "] load -> " + GameApi.LoadSave(_config.Autoload)); }
                            catch (Exception ex) { Debug.LogError("[" + ModId + "] autoload failed: " + ex); }
                        }
                        else
                        {
                            Debug.Log("[" + ModId + "] autoload waiting (scene=" + scene + " t=" + Time.realtimeSinceStartup.ToString("0") + ")");
                            _autoloadNext = Time.realtimeSinceStartup + 5f;
                        }
                    }
                }
            }
        }

        private void OnDestroy()
        {
            if (_server != null) _server.Stop();
        }
    }

    /// <summary>Server settings, read from the ic10go user folder.</summary>
    internal sealed class TestbenchConfig
    {
        public string Host = "127.0.0.1";
        public int Port = 7800;
        public string Autoload = "";
        public int AutoloadDelay = 90;

        public static TestbenchConfig Load()
        {
            var cfg = new TestbenchConfig();
            try
            {
                string file = Path.Combine(OutputFolder(), "testbench.json");
                if (File.Exists(file))
                {
                    var o = JObject.Parse(File.ReadAllText(file));
                    if (o["host"] != null) cfg.Host = (string)o["host"];
                    if (o["port"] != null) cfg.Port = (int)o["port"];
                    if (o["autoload"] != null) cfg.Autoload = (string)o["autoload"];
                    if (o["autoloadDelay"] != null) cfg.AutoloadDelay = (int)o["autoloadDelay"];
                }
            }
            catch (Exception ex)
            {
                Debug.LogError("[" + TestbenchPlugin.ModId + "] bad testbench.json, using defaults: " + ex.Message);
            }
            return cfg;
        }

        /// <summary>My Games/Stationeers/ic10go, the same folder the exporter writes to.</summary>
        public static string OutputFolder()
        {
            string docs = Environment.GetFolderPath(Environment.SpecialFolder.MyDocuments);
            return Path.Combine(docs, "My Games", "Stationeers", "ic10go");
        }
    }
}
