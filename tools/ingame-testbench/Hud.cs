// Hud draws a small on-screen overlay with the local player's position and view
// angles, plus the distance/direction to an optional target. It exists so a
// player can walk to a chip the testbench located, e.g.
//   ic10c testbench hud --chip 118
// then follow the compass arrow.
//
// Enabled by default; F8 toggles it, F9 clears the target (see TestbenchPlugin).
// State is process-local and set through the `hud` protocol command. Nothing is
// drawn until a world is loaded (Player() returns false at the menu).

using System;
using Assets.Scripts;
using Assets.Scripts.Objects.Entities;
using Newtonsoft.Json.Linq;
using UnityEngine;

namespace Ic10Go.Testbench
{
    internal static class Hud
    {
        public static bool Enabled = true;
        public static bool HasTarget;
        public static Vector3 Target;
        public static string TargetLabel = "";
        /// <summary>When set, the target is this player (its position is read live
        /// each frame, since players move).</summary>
        public static Human TargetHuman;
        /// <summary>Degrees added to the view heading, if a client's compass uses
        /// a different zero. Tunable at runtime with `hud --offset N`.</summary>
        public static float HeadingOffset;

        private static Camera _cam;
        private static Texture2D _bg;
        private static Texture2D _arrow;
        private static GUIStyle _label;

        public static void SetTarget(Vector3 p, string label)
        {
            TargetHuman = null;
            Target = p;
            TargetLabel = label ?? "";
            HasTarget = true;
            Enabled = true;
        }

        /// <summary>Tracks a player: the target position is read live each frame.</summary>
        public static void SetPlayer(Human human, string label)
        {
            TargetHuman = human;
            if (human != null)
            {
                try { Target = human.transform.position; } catch { Target = Vector3.zero; }
            }
            TargetLabel = label ?? "";
            HasTarget = human != null;
            Enabled = true;
        }

        public static void Clear()
        {
            TargetHuman = null;
            HasTarget = false;
            TargetLabel = "";
        }

        /// <summary>The current target position: a live player when one is tracked,
        /// else the fixed point. False when nothing is tracked.</summary>
        public static bool TryTarget(out Vector3 pos)
        {
            var h = TargetHuman;
            if (h != null)
            {
                try { pos = h.transform.position; return true; } catch { }
            }
            if (HasTarget) { pos = Target; return true; }
            pos = Vector3.zero;
            return false;
        }

        public static void Toggle() => Enabled = !Enabled;

        /// <summary>Reads the local player. False when no world/player is loaded.</summary>
        public static bool Player(out Vector3 pos, out float yaw, out float pitch)
        {
            pos = Vector3.zero;
            yaw = 0f;
            pitch = 0f;
            try
            {
                var human = Human.LocalHuman;
                if (human == null) return false;
                pos = human.transform.position;
                // Match the in-game compass (StationeersUIMod):
                //   heading = CameraController.CurrentCamera.eulerAngles.y + 180
                // The +180 is that UI's convention; Camera.main is not used
                // because it can resolve to the portrait camera instead.
                Camera cam = null;
                try { cam = CameraController.CurrentCamera; } catch { }
                if (cam == null)
                {
                    if (_cam == null) _cam = Camera.main;
                    cam = _cam;
                }
                if (cam != null)
                {
                    float e = cam.transform.eulerAngles.y;
                    yaw = Mathf.Repeat(e + 180f + HeadingOffset, 360f);
                    pitch = Mathf.DeltaAngle(0f, cam.transform.eulerAngles.x);
                }
                return true;
            }
            catch
            {
                return false;
            }
        }

        public static JObject State()
        {
            var o = new JObject { ["on"] = Enabled, ["offset"] = HeadingOffset };
            Vector3 p;
            float yaw, pitch;
            if (Player(out p, out yaw, out pitch))
            {
                o["player"] = new JObject
                {
                    ["x"] = Math.Round(p.x, 2),
                    ["y"] = Math.Round(p.y, 2),
                    ["z"] = Math.Round(p.z, 2),
                    ["yaw"] = Math.Round(yaw, 1),
                    ["pitch"] = Math.Round(pitch, 1),
                };
            }
            Vector3 tp;
            if (TryTarget(out tp))
            {
                var t = new JObject { ["x"] = Math.Round(tp.x, 2), ["y"] = Math.Round(tp.y, 2), ["z"] = Math.Round(tp.z, 2) };
                if (!string.IsNullOrEmpty(TargetLabel)) t["label"] = TargetLabel;
                if (TargetHuman != null) t["player"] = true;
                o["target"] = t;
            }
            return o;
        }

        public static void Draw()
        {
            if (!Enabled) return;
            Vector3 p;
            float yaw, pitch;
            if (!Player(out p, out yaw, out pitch)) return;
            Ensure();

            string pose = string.Format("X {0:0.0}   Y {1:0.0}   Z {2:0.0}", p.x, p.y, p.z);
            string look = string.Format("朝向 {0:0}°   俯仰 {1:0}°", yaw, pitch);

            Vector3 target;
            bool hasTarget = TryTarget(out target);

            float width = 226f;
            float height = 54f;               // position + heading
            if (hasTarget) height += 74f;     // target lines + a compass row
            var rect = new Rect(10f, 10f, width, height);
            GUI.DrawTexture(rect, _bg);

            float x = rect.x + 10f;
            float y = rect.y + 8f;
            Label(x, ref y, 18f, pose);
            Label(x, ref y, 18f, look);

            if (hasTarget)
            {
                float dx = target.x - p.x;
                float dy = target.y - p.y;
                float dz = target.z - p.z;
                float dist = Mathf.Sqrt(dx * dx + dy * dy + dz * dz);
                float bearing = Mathf.Atan2(dx, dz) * Mathf.Rad2Deg; // 0 = +Z
                float rel = Mathf.DeltaAngle(yaw, bearing);          // signed, right positive

                string name = string.IsNullOrEmpty(TargetLabel) ? "目标" : TargetLabel;
                Label(x, ref y, 18f, string.Format("{0}  {1:0.0} m  {2}", name, dist, Direction(rel)));
                Label(x, ref y, 16f, string.Format("高差 {0:+0.0;-0.0;0.0} m", dy));

                // The compass lives on its own centred row so it never covers
                // the text above it.
                var box = new Rect(rect.x + (width - 40f) / 2f, y + 3f, 40f, 40f);
                DrawArrow(box, rel);
            }
        }

        private static void Label(float x, ref float y, float line, string text)
        {
            GUI.Label(new Rect(x, y, 220f, line), text, _label);
            y += line;
        }

        /// <summary>Draws the up-pointing arrow rotated to the target's relative bearing.</summary>
        private static void DrawArrow(Rect r, float rel)
        {
            var pivot = new Vector2(r.x + r.width / 2f, r.y + r.height / 2f);
            var prev = GUI.matrix;
            GUIUtility.RotateAroundPivot(rel, pivot);
            GUI.DrawTexture(r, _arrow);
            GUI.matrix = prev;
        }

        private static string Direction(float rel)
        {
            float a = Mathf.Abs(rel);
            if (a < 15f) return "正前";
            if (a < 60f) return rel > 0 ? "右前" : "左前";
            if (a < 120f) return rel > 0 ? "右侧" : "左侧";
            if (a < 165f) return rel > 0 ? "右后" : "左后";
            return "正后";
        }

        private static void Ensure()
        {
            if (_label != null) return;
            _bg = Solid(new Color(0f, 0f, 0f, 0.62f));
            _arrow = Arrow(22);
            _label = new GUIStyle(GUI.skin.label) { fontSize = 13, alignment = TextAnchor.UpperLeft };
            _label.normal.textColor = Color.white;
        }

        private static Texture2D Solid(Color c)
        {
            var t = new Texture2D(1, 1, TextureFormat.RGBA32, false);
            t.SetPixel(0, 0, c);
            t.Apply();
            return t;
        }

        /// <summary>An n-by-n white triangle pointing up (tip at the last row).</summary>
        private static Texture2D Arrow(int n)
        {
            var t = new Texture2D(n, n, TextureFormat.RGBA32, false);
            var clear = new Color(0f, 0f, 0f, 0f);
            var white = new Color(1f, 1f, 1f, 0.95f);
            for (int y = 0; y < n; y++)
            {
                // Texture y grows upward: wide at the bottom, tip at the top.
                float half = (n - y) / (float)n * (n / 2f);
                for (int x = 0; x < n; x++)
                {
                    float cx = x - (n - 1) / 2f;
                    bool on = Mathf.Abs(cx) <= half;
                    t.SetPixel(x, y, on ? white : clear);
                }
            }
            t.Apply();
            return t;
        }
    }
}
