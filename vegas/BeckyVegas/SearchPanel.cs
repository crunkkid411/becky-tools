using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.Text;
using System.Threading;
using System.Windows.Forms;
using ScriptPortal.Vegas;

namespace BeckyVegas
{
    // SearchPanel is the dockable "Becky Search" window. Everything that touches VEGAS
    // runs in UI event handlers (the main thread); becky's tools run on a background
    // thread and hand results back through UiThread.Post, so VEGAS never freezes
    // while a search or a transcription runs.
    //
    // Colours are deliberately high-contrast (Jordan has impaired vision; bright
    // colour on dark is an aid, see ACCESSIBILITY.md) and text is large.
    internal sealed class SearchPanel : DockableControl
    {
        static readonly Color Bg = Color.FromArgb(28, 28, 28);
        static readonly Color InputBg = Color.FromArgb(12, 12, 12);
        static readonly Color RowBg = Color.FromArgb(22, 22, 22);
        static readonly Color RowAltBg = Color.FromArgb(36, 36, 36);
        static readonly Color RowSelBg = Color.FromArgb(0, 70, 110);
        static readonly Color Fg = Color.White;
        static readonly Color Muted = Color.FromArgb(200, 200, 200);
        static readonly Color Cyan = Color.FromArgb(0, 220, 255);
        static readonly Color Amber = Color.FromArgb(255, 196, 0);
        static readonly Color Green = Color.FromArgb(110, 230, 110);
        static readonly Color Red = Color.FromArgb(255, 110, 110);
        static readonly Color ButtonBg = Color.FromArgb(58, 58, 58);

        readonly Vegas vegas;
        readonly Font baseFont = new Font("Segoe UI", 11f);
        readonly Font bigFont = new Font("Segoe UI", 14f);
        readonly Font rowHeadFont = new Font("Segoe UI", 11f, FontStyle.Bold);
        readonly Font rowTextFont = new Font("Segoe UI", 13f);

        Button timelineButton, folderButton, browseButton, searchButton, transcribeButton, stopButton;
        TextBox folderBox, queryBox;
        Label statusLabel, hintLabel;
        ListBox resultsList;
        TableLayoutPanel folderRow;

        readonly List<Row> rows = new List<Row>();
        List<MediaFile> untranscribed = new List<MediaFile>();
        bool folderMode;
        bool busy;
        volatile bool stopRequested;
        string lastQuery = "";

        sealed class Row
        {
            public Hit Hit;
            public string When;
            public string Where;
            public Color WhenColor;
        }

        internal SearchPanel(Vegas vegas) : base(BeckyVegasModule.PanelInstanceName)
        {
            this.vegas = vegas;
            DisplayName = "Becky Search";
            DefaultDockWindowStyle = DockWindowStyle.Floating;
            DefaultFloatingSize = new Size(620, 760);
            BuildUi();
            LoadSettings();
            ApplyMode();
        }

        // ---------------------------------------------------------------- layout

        void BuildUi()
        {
            BackColor = Bg;
            ForeColor = Fg;
            Font = baseFont;
            AllowDrop = true;
            DragEnter += OnDragEnter;
            DragDrop += OnDragDrop;

            TableLayoutPanel root = new TableLayoutPanel();
            root.Dock = DockStyle.Fill;
            root.ColumnCount = 1;
            root.Padding = new Padding(10);
            root.BackColor = Bg;
            root.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100f));

            FlowLayoutPanel scopeRow = new FlowLayoutPanel { AutoSize = true, Dock = DockStyle.Fill, Margin = new Padding(0, 0, 0, 6), WrapContents = true };
            timelineButton = MakeButton("Search my timeline", (s, e) => SetMode(false));
            folderButton = MakeButton("Search a folder", (s, e) => SetMode(true));
            scopeRow.Controls.Add(timelineButton);
            scopeRow.Controls.Add(folderButton);

            folderRow = TwoColumnRow();
            folderBox = MakeTextBox(baseFont);
            folderBox.KeyDown += (s, e) => { if (e.KeyCode == Keys.Enter) { e.SuppressKeyPress = true; StartSearch(); } };
            browseButton = MakeButton("Choose folder...", (s, e) => ChooseFolder());
            folderRow.Controls.Add(folderBox, 0, 0);
            folderRow.Controls.Add(browseButton, 1, 0);

            TableLayoutPanel queryRow = TwoColumnRow();
            queryBox = MakeTextBox(bigFont);
            queryBox.KeyDown += (s, e) => { if (e.KeyCode == Keys.Enter) { e.SuppressKeyPress = true; StartSearch(); } };
            searchButton = MakeButton("Search", (s, e) => StartSearch());
            searchButton.Font = bigFont;
            searchButton.BackColor = Cyan;
            searchButton.ForeColor = Color.Black;
            queryRow.Controls.Add(queryBox, 0, 0);
            queryRow.Controls.Add(searchButton, 1, 0);

            statusLabel = new Label { AutoSize = true, Dock = DockStyle.Fill, ForeColor = Muted, Margin = new Padding(2, 6, 2, 4) };
            FlowLayoutPanel jobRow = new FlowLayoutPanel { AutoSize = true, Dock = DockStyle.Fill, Margin = new Padding(0), WrapContents = true };
            transcribeButton = MakeButton("Transcribe", (s, e) => StartTranscribe());
            transcribeButton.BackColor = Amber;
            transcribeButton.ForeColor = Color.Black;
            stopButton = MakeButton("Stop", (s, e) => { stopRequested = true; SetStatus("Stopping...", Amber); });
            stopButton.BackColor = Red;
            stopButton.ForeColor = Color.Black;
            jobRow.Controls.Add(transcribeButton);
            jobRow.Controls.Add(stopButton);

            resultsList = new ListBox();
            resultsList.Dock = DockStyle.Fill;
            resultsList.DrawMode = DrawMode.OwnerDrawVariable;
            resultsList.BackColor = RowBg;
            resultsList.ForeColor = Fg;
            resultsList.BorderStyle = BorderStyle.FixedSingle;
            resultsList.IntegralHeight = false;
            resultsList.Margin = new Padding(0, 6, 0, 6);
            resultsList.MeasureItem += MeasureRow;
            resultsList.DrawItem += DrawRow;
            resultsList.DoubleClick += (s, e) => Safe("double-click", () => DefaultAction(true));
            resultsList.KeyDown += (s, e) => { if (e.KeyCode == Keys.Enter) { e.SuppressKeyPress = true; Safe("enter", () => DefaultAction(false)); } };
            resultsList.MouseDown += OnResultsMouseDown;
            resultsList.Resize += (s, e) => RefillList(); // row heights depend on the width

            hintLabel = new Label
            {
                AutoSize = true,
                Dock = DockStyle.Fill,
                ForeColor = Muted,
                Margin = new Padding(2, 0, 2, 6),
                Text = "Double-click a result: timeline = jump to it, folder = add that line at the cursor. Right-click for more."
            };

            FlowLayoutPanel toolsRow = new FlowLayoutPanel { AutoSize = true, Dock = DockStyle.Fill, Margin = new Padding(0), WrapContents = true };
            toolsRow.Controls.Add(MakeButton("Cut silence in selection", (s, e) => Safe("BeckyCut", () => RunBeckyScript("BeckyCut.cs"))));
            toolsRow.Controls.Add(MakeButton("Caption selection", (s, e) => Safe("BeckyCaptions", () => RunBeckyScript("BeckyCaptions.cs"))));

            root.RowCount = 7;
            root.RowStyles.Add(new RowStyle(SizeType.AutoSize));
            root.RowStyles.Add(new RowStyle(SizeType.AutoSize));
            root.RowStyles.Add(new RowStyle(SizeType.AutoSize));
            root.RowStyles.Add(new RowStyle(SizeType.AutoSize));
            root.RowStyles.Add(new RowStyle(SizeType.Percent, 100f));
            root.RowStyles.Add(new RowStyle(SizeType.AutoSize));
            root.RowStyles.Add(new RowStyle(SizeType.AutoSize));
            root.Controls.Add(scopeRow, 0, 0);
            root.Controls.Add(folderRow, 0, 1);
            root.Controls.Add(queryRow, 0, 2);
            TableLayoutPanel statusRow = new TableLayoutPanel { AutoSize = true, Dock = DockStyle.Fill, ColumnCount = 1, Margin = new Padding(0) };
            statusRow.Controls.Add(statusLabel, 0, 0);
            statusRow.Controls.Add(jobRow, 0, 1);
            root.Controls.Add(statusRow, 0, 3);
            root.Controls.Add(resultsList, 0, 4);
            root.Controls.Add(hintLabel, 0, 5);
            root.Controls.Add(toolsRow, 0, 6);
            Controls.Add(root);
            // Labels only wrap when they know how wide they may get; without this the
            // status line ran off the edge of the panel mid-sentence.
            root.SizeChanged += (s, e) => FitLabels(root);
            FitLabels(root);

            foreach (Control c in new Control[] { root, resultsList, queryBox, folderBox, statusLabel })
            {
                c.AllowDrop = true;
                c.DragEnter += OnDragEnter;
                c.DragDrop += OnDragDrop;
            }

            SetStatus("Type what was said, then press Enter.", Muted);
            UpdateJobButtons();
        }

        void FitLabels(Control root)
        {
            int width = Math.Max(120, root.ClientSize.Width - root.Padding.Horizontal - 8);
            statusLabel.MaximumSize = new Size(width, 0);
            hintLabel.MaximumSize = new Size(width, 0);
        }

        static TableLayoutPanel TwoColumnRow()
        {
            TableLayoutPanel row = new TableLayoutPanel { AutoSize = true, Dock = DockStyle.Fill, ColumnCount = 2, Margin = new Padding(0, 0, 0, 6) };
            row.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100f));
            row.ColumnStyles.Add(new ColumnStyle(SizeType.AutoSize));
            return row;
        }

        Button MakeButton(string text, EventHandler onClick)
        {
            Button b = new Button();
            b.Text = text;
            b.AutoSize = true;
            b.AutoSizeMode = AutoSizeMode.GrowAndShrink;
            b.FlatStyle = FlatStyle.Flat;
            b.FlatAppearance.BorderColor = Color.FromArgb(120, 120, 120);
            b.BackColor = ButtonBg;
            b.ForeColor = Fg;
            b.Padding = new Padding(8, 3, 8, 3);
            b.Margin = new Padding(0, 0, 8, 0);
            b.UseVisualStyleBackColor = false;
            b.Click += onClick;
            return b;
        }

        static TextBox MakeTextBox(Font font)
        {
            return new TextBox
            {
                Dock = DockStyle.Fill,
                Font = font,
                BackColor = InputBg,
                ForeColor = Fg,
                BorderStyle = BorderStyle.FixedSingle,
                Margin = new Padding(0, 2, 8, 0)
            };
        }

        void SetMode(bool folder)
        {
            if (busy)
            {
                return;
            }
            folderMode = folder;
            ApplyMode();
            SaveSettings();
            ClearResults();
            queryBox.Focus();
        }

        void ApplyMode()
        {
            folderRow.Visible = folderMode;
            PaintScope(timelineButton, !folderMode);
            PaintScope(folderButton, folderMode);
            if (folderMode && folderBox.Text.Trim().Length == 0)
            {
                folderBox.Text = DefaultFolder();
            }
        }

        void PaintScope(Button b, bool active)
        {
            b.BackColor = active ? Cyan : ButtonBg;
            b.ForeColor = active ? Color.Black : Fg;
            b.Font = active ? rowHeadFont : baseFont;
        }

        void SetStatus(string text, Color color)
        {
            statusLabel.Text = text;
            statusLabel.ForeColor = color;
        }

        void UpdateJobButtons()
        {
            transcribeButton.Visible = !busy && untranscribed.Count > 0;
            transcribeButton.Text = "Transcribe " + untranscribed.Count + (untranscribed.Count == 1 ? " clip" : " clips");
            stopButton.Visible = busy;
            searchButton.Enabled = !busy;
            timelineButton.Enabled = !busy;
            folderButton.Enabled = !busy;
        }

        // ---------------------------------------------------------------- search

        void StartSearch()
        {
            if (busy)
            {
                return;
            }
            string query = queryBox.Text.Trim();
            if (query.Length == 0)
            {
                SetStatus("Type what was said first.", Amber);
                queryBox.Focus();
                return;
            }
            bool searchFolder = folderMode;
            string folder = folderBox.Text.Trim().Trim('"');
            Dictionary<string, object> timelineDoc = null;
            if (searchFolder)
            {
                if (!Directory.Exists(folder))
                {
                    SetStatus("That folder does not exist. Click \"Choose folder...\".", Red);
                    return;
                }
                SaveSettings();
            }
            else
            {
                timelineDoc = ProjectOps.TimelineDoc(vegas, false);
                if (((List<Dictionary<string, object>>)timelineDoc["events"]).Count == 0)
                {
                    ClearResults();
                    SetStatus("No clips on the timeline yet. Use \"Search a folder\".", Amber);
                    return;
                }
            }

            lastQuery = query;
            BeginJob(searchFolder ? "Searching " + folder + " ..." : "Searching the clips on your timeline ...");
            RunInBackground("search", () =>
            {
                SearchResult result = searchFolder
                    ? BeckyTools.SearchFolder(folder, query, () => stopRequested)
                    : BeckyTools.SearchTimeline(timelineDoc, query, () => stopRequested);
                UiThread.Post(() => { if (!IsDisposed) { EndJob(); ShowResults(result, searchFolder, query); } });
            });
        }

        void ShowResults(SearchResult result, bool searchFolder, string query)
        {
            rows.Clear();
            int onTimeline = 0, cutOut = 0;
            foreach (Hit h in result.Hits)
            {
                Row row = new Row { Hit = h };
                if (h.FromTimeline && h.OnTimeline)
                {
                    onTimeline++;
                    row.When = ProjectOps.Tc(h.Timeline).ToPositionString();
                    row.WhenColor = Cyan;
                    row.Where = h.Name + "  (" + Clock(h.SourceStart) + " into the clip)";
                }
                else if (h.FromTimeline)
                {
                    cutOut++;
                    row.When = "cut from your edit";
                    row.WhenColor = Amber;
                    row.Where = h.Name + "  (" + Clock(h.SourceStart) + " into the clip)";
                }
                else
                {
                    row.When = Clock(h.SourceStart);
                    row.WhenColor = Green;
                    row.Where = h.Name + "  -  " + (Path.GetDirectoryName(h.Source) ?? "");
                }
                rows.Add(row);
            }
            RefillList();
            untranscribed = result.Untranscribed();

            StringBuilder msg = new StringBuilder();
            if (searchFolder)
            {
                msg.Append(result.Hits.Count == 0
                    ? "Nothing in that folder says \"" + query + "\"."
                    : result.Hits.Count + (result.Hits.Count == 1 ? " line found." : " lines found."));
            }
            else if (onTimeline == 0 && cutOut == 0)
            {
                msg.Append("No clip on your timeline says \"" + query + "\".");
            }
            else
            {
                msg.Append(onTimeline + (onTimeline == 1 ? " place" : " places") + " on your timeline.");
                if (cutOut > 0)
                {
                    msg.Append(" " + cutOut + " more " + (cutOut == 1 ? "was" : "were") + " cut from the edit.");
                }
            }
            if (untranscribed.Count > 0)
            {
                msg.Append(" " + untranscribed.Count + (untranscribed.Count == 1 ? " clip has" : " clips have") + " no transcript yet.");
            }
            SetStatus(msg.ToString(), result.Hits.Count > 0 ? Green : Amber);
            UpdateJobButtons();
            if (rows.Count > 0)
            {
                resultsList.SelectedIndex = 0;
            }
        }

        void ClearResults()
        {
            rows.Clear();
            untranscribed = new List<MediaFile>();
            RefillList();
            UpdateJobButtons();
            SetStatus("Type what was said, then press Enter.", Muted);
        }

        // RefillList re-adds every row so the ListBox asks for fresh row heights.
        void RefillList()
        {
            if (resultsList == null)
            {
                return;
            }
            int selected = resultsList.SelectedIndex;
            resultsList.BeginUpdate();
            resultsList.Items.Clear();
            foreach (Row r in rows)
            {
                resultsList.Items.Add(r);
            }
            if (selected >= 0 && selected < rows.Count)
            {
                resultsList.SelectedIndex = selected;
            }
            resultsList.EndUpdate();
        }

        static string Clock(double seconds)
        {
            TimeSpan t = TimeSpan.FromSeconds(Math.Max(0, seconds));
            return t.TotalHours >= 1
                ? string.Format("{0}:{1:00}:{2:00}", (int)t.TotalHours, t.Minutes, t.Seconds)
                : string.Format("{0}:{1:00}.{2}", t.Minutes, t.Seconds, t.Milliseconds / 100);
        }

        // ---------------------------------------------------------------- drawing

        void MeasureRow(object sender, MeasureItemEventArgs e)
        {
            if (e.Index < 0 || e.Index >= rows.Count)
            {
                return;
            }
            int width = Math.Max(120, resultsList.ClientSize.Width - 20);
            Size text = TextRenderer.MeasureText(rows[e.Index].Hit.Text, rowTextFont, new Size(width, 2000), TextFormatFlags.WordBreak | TextFormatFlags.NoPadding);
            e.ItemHeight = Math.Min(255, rowHeadFont.Height + text.Height + 16);
        }

        void DrawRow(object sender, DrawItemEventArgs e)
        {
            if (e.Index < 0 || e.Index >= rows.Count)
            {
                return;
            }
            Row row = rows[e.Index];
            bool selected = (e.State & DrawItemState.Selected) == DrawItemState.Selected;
            using (SolidBrush b = new SolidBrush(selected ? RowSelBg : (e.Index % 2 == 0 ? RowBg : RowAltBg)))
            {
                e.Graphics.FillRectangle(b, e.Bounds);
            }
            Rectangle r = Rectangle.Inflate(e.Bounds, -10, -6);
            TextFormatFlags oneLine = TextFormatFlags.NoPadding | TextFormatFlags.EndEllipsis | TextFormatFlags.SingleLine;
            TextRenderer.DrawText(e.Graphics, row.When, rowHeadFont, new Point(r.X, r.Y), row.WhenColor, TextFormatFlags.NoPadding);
            int x = r.X + TextRenderer.MeasureText(row.When, rowHeadFont, Size.Empty, TextFormatFlags.NoPadding).Width + 12;
            TextRenderer.DrawText(e.Graphics, row.Where, baseFont, new Rectangle(x, r.Y, Math.Max(10, r.Right - x), rowHeadFont.Height), Muted, oneLine);
            Rectangle textRect = new Rectangle(r.X, r.Y + rowHeadFont.Height + 4, r.Width, Math.Max(10, r.Bottom - r.Y - rowHeadFont.Height - 4));
            TextRenderer.DrawText(e.Graphics, row.Hit.Text, rowTextFont, textRect, Fg, TextFormatFlags.NoPadding | TextFormatFlags.WordBreak | TextFormatFlags.EndEllipsis);
            if (selected)
            {
                using (Pen pen = new Pen(Cyan, 2f))
                {
                    e.Graphics.DrawRectangle(pen, e.Bounds.X + 1, e.Bounds.Y + 1, e.Bounds.Width - 3, e.Bounds.Height - 3);
                }
            }
        }

        // ---------------------------------------------------------------- actions

        Row SelectedRow()
        {
            int i = resultsList.SelectedIndex;
            return i >= 0 && i < rows.Count ? rows[i] : null;
        }

        void DefaultAction(bool fromMouse)
        {
            Row row = SelectedRow();
            if (row == null)
            {
                return;
            }
            Hit h = row.Hit;
            if (h.FromTimeline && h.OnTimeline)
            {
                Jump(h, fromMouse);
            }
            else if (h.FromTimeline)
            {
                SetStatus("That line was cut from your edit. Right-click it to add it back at the cursor.", Amber);
            }
            else
            {
                InsertAtCursor(h, false);
            }
        }

        void Jump(Hit h, bool giveTimelineFocus)
        {
            double selected = ProjectOps.JumpTo(vegas, h.Timeline, h.TimelineEnd);
            string at = vegas.Transport.CursorPosition.ToPositionString();
            SetStatus(selected > 0
                ? "Cursor on that line at " + at + ", line selected. Press Space to hear it."
                : "Cursor on that line at " + at + ".", Green);
            if (giveTimelineFocus)
            {
                SetFocusToMainTrackView(); // Space now plays it straight away
            }
        }

        void InsertAtCursor(Hit h, bool wholeClip)
        {
            double at = ProjectOps.Sec(vegas.Transport.CursorPosition);
            double end = wholeClip
                ? ProjectOps.InsertClip(vegas, h.Source, 0, 0, at)
                : ProjectOps.InsertClip(vegas, h.Source, h.SourceStart, h.SourceEnd, at);
            vegas.Transport.CursorPosition = ProjectOps.Tc(end);
            vegas.Transport.ViewCursor(true);
            SetStatus((wholeClip ? "Clip added at " : "Line added at ") + ProjectOps.Tc(at).ToPositionString() +
                      " on the \"" + ProjectOps.PullsTrackName + "\" tracks. Ctrl+Z undoes it.", Green);
        }

        void OnResultsMouseDown(object sender, MouseEventArgs e)
        {
            if (e.Button != MouseButtons.Right)
            {
                return;
            }
            int index = resultsList.IndexFromPoint(e.Location);
            if (index < 0 || index >= rows.Count)
            {
                return;
            }
            resultsList.SelectedIndex = index;
            Hit h = rows[index].Hit;
            ContextMenuStrip menu = new ContextMenuStrip();
            menu.Font = baseFont;
            menu.BackColor = RowAltBg;
            menu.ForeColor = Fg;
            menu.ShowImageMargin = false;
            ToolStripItem jump = menu.Items.Add("Jump to this line", null, (s, a) => Safe("jump", () => Jump(h, true)));
            jump.Enabled = h.FromTimeline && h.OnTimeline;
            menu.Items.Add("Put this line at the cursor", null, (s, a) => Safe("insert line", () => InsertAtCursor(h, false)));
            menu.Items.Add("Put the whole clip at the cursor", null, (s, a) => Safe("insert clip", () => InsertAtCursor(h, true)));
            ToolStripItem marker = menu.Items.Add("Add a marker here", null, (s, a) => Safe("marker", () =>
            {
                string label = h.Text.Length > 60 ? h.Text.Substring(0, 57) + "..." : h.Text;
                ProjectOps.AddMarker(vegas, h.Timeline, label);
                SetStatus("Marker added at " + ProjectOps.Tc(h.Timeline).ToPositionString() + ". Ctrl+Z undoes it.", Green);
            }));
            marker.Enabled = h.FromTimeline && h.OnTimeline;
            menu.Items.Add(new ToolStripSeparator());
            menu.Items.Add("Copy the words", null, (s, a) => Safe("copy", () => Clipboard.SetText(h.Text)));
            menu.Items.Add("Show the file in Explorer", null, (s, a) => Safe("explorer", () => Process.Start("explorer.exe", "/select," + BeckyTools.Quote(h.Source))));
            menu.Closed += (s, a) => menu.BeginInvoke((MethodInvoker)menu.Dispose);
            menu.Show(resultsList, e.Location);
        }

        void RunBeckyScript(string scriptName)
        {
            string perUser = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.MyDocuments), "Vegas Script Menu", scriptName);
            string repo = Path.Combine(@"X:\AI-2\becky-tools\vegas", scriptName);
            string path = File.Exists(perUser) ? perUser : repo;
            if (!File.Exists(path))
            {
                SetStatus(scriptName + " is not installed. Run \"Install Vegas Scripts.bat\".", Red);
                return;
            }
            string name = Path.GetFileNameWithoutExtension(scriptName);
            SetStatus("Running " + name + " on your selection ...", Cyan);
            statusLabel.Refresh(); // RunScriptFile holds the main thread until the script is done
            vegas.RunScriptFile(path);
            SetStatus(name + " finished. Ctrl+Z undoes it.", Green);
        }

        // ---------------------------------------------------------------- transcription

        void StartTranscribe()
        {
            if (busy || untranscribed.Count == 0)
            {
                return;
            }
            List<MediaFile> todo = new List<MediaFile>(untranscribed);
            BeginJob("Starting transcription ...");
            RunInBackground("transcribe", () =>
            {
                int made = 0;
                List<string> failed = new List<string>();
                for (int i = 0; i < todo.Count && !stopRequested; i++)
                {
                    MediaFile m = todo[i];
                    string line = "Transcribing " + (i + 1) + " of " + todo.Count + ": " + m.Name + " - long clips take a few minutes.";
                    UiThread.Post(() => { if (!IsDisposed) { SetStatus(line, Amber); } });
                    try
                    {
                        BeckyTools.Transcribe(m.Path, () => stopRequested);
                        made++;
                    }
                    catch (OperationCanceledException)
                    {
                        break;
                    }
                    catch (Exception ex)
                    {
                        Log.Error("transcribe " + m.Path, ex);
                        failed.Add(m.Name + ": " + ex.Message);
                    }
                }
                bool stopped = stopRequested;
                UiThread.Post(() =>
                {
                    if (IsDisposed)
                    {
                        return;
                    }
                    EndJob();
                    string summary = "Transcribed " + made + " of " + todo.Count + (stopped ? " (stopped)." : ".");
                    if (failed.Count > 0)
                    {
                        summary += " Failed: " + string.Join("; ", failed.ToArray());
                    }
                    SetStatus(summary, failed.Count > 0 ? Red : Green);
                    if (made > 0 && lastQuery.Length > 0)
                    {
                        queryBox.Text = lastQuery;
                        StartSearch();
                    }
                });
            });
        }

        // ---------------------------------------------------------------- jobs

        void BeginJob(string message)
        {
            busy = true;
            stopRequested = false;
            SetStatus(message, Cyan);
            UpdateJobButtons();
        }

        void EndJob()
        {
            busy = false;
            stopRequested = false;
            UpdateJobButtons();
        }

        void RunInBackground(string name, Action work)
        {
            Thread t = new Thread(() =>
            {
                try
                {
                    work();
                }
                catch (OperationCanceledException)
                {
                    UiThread.Post(() => { if (!IsDisposed) { EndJob(); SetStatus("Stopped.", Amber); } });
                }
                catch (Exception ex)
                {
                    Log.Error(name, ex);
                    UiThread.Post(() => { if (!IsDisposed) { EndJob(); SetStatus(ex.Message, Red); } });
                }
            });
            t.IsBackground = true;
            t.Name = "BeckyVegas " + name;
            t.Start();
        }

        void Safe(string what, Action action)
        {
            try
            {
                action();
            }
            catch (Exception ex)
            {
                Log.Error(what, ex);
                Exception shown = ex is ApplicationException && ex.InnerException != null ? ex.InnerException : ex;
                SetStatus(shown.Message, Red);
            }
        }

        // ---------------------------------------------------------------- folder choice

        void ChooseFolder()
        {
            Safe("choose folder", () =>
            {
                string picked = FolderPicker.Pick(vegas.MainWindow.Handle, "Choose the footage folder to search", folderBox.Text.Trim());
                if (!string.IsNullOrEmpty(picked))
                {
                    folderBox.Text = picked;
                    SaveSettings();
                    ClearResults();
                    queryBox.Focus();
                }
            });
        }

        void OnDragEnter(object sender, DragEventArgs e)
        {
            e.Effect = e.Data.GetDataPresent(DataFormats.FileDrop) ? DragDropEffects.Copy : DragDropEffects.None;
        }

        void OnDragDrop(object sender, DragEventArgs e)
        {
            Safe("drop", () =>
            {
                string[] paths = e.Data.GetData(DataFormats.FileDrop) as string[];
                if (paths == null || paths.Length == 0 || busy)
                {
                    return;
                }
                string folder = Directory.Exists(paths[0]) ? paths[0] : Path.GetDirectoryName(paths[0]);
                folderMode = true;
                folderBox.Text = folder;
                ApplyMode();
                SaveSettings();
                ClearResults();
                SetStatus("Folder set. Type what was said, then press Enter.", Cyan);
                queryBox.Focus();
            });
        }

        string DefaultFolder()
        {
            try
            {
                if (vegas.Project != null && !string.IsNullOrEmpty(vegas.Project.FilePath))
                {
                    return Path.GetDirectoryName(vegas.Project.FilePath);
                }
            }
            catch (Exception ex)
            {
                Log.Error("default folder", ex);
            }
            return "";
        }

        // ---------------------------------------------------------------- settings

        static string SettingsPath
        {
            get { return Path.Combine(Log.Dir, "search-panel.json"); }
        }

        void LoadSettings()
        {
            try
            {
                if (!File.Exists(SettingsPath))
                {
                    return;
                }
                Dictionary<string, object> d = BeckyTools.Json().DeserializeObject(File.ReadAllText(SettingsPath)) as Dictionary<string, object>;
                folderBox.Text = BeckyTools.Str(d, "folder");
                folderMode = BeckyTools.Bool(d, "folder_mode");
            }
            catch (Exception ex)
            {
                Log.Error("load settings", ex);
            }
        }

        void SaveSettings()
        {
            try
            {
                Directory.CreateDirectory(Log.Dir);
                Dictionary<string, object> d = new Dictionary<string, object>();
                d["folder"] = folderBox.Text.Trim();
                d["folder_mode"] = folderMode;
                File.WriteAllText(SettingsPath, BeckyTools.Json().Serialize(d), new UTF8Encoding(false));
            }
            catch (Exception ex)
            {
                Log.Error("save settings", ex);
            }
        }

        protected override void Dispose(bool disposing)
        {
            if (disposing)
            {
                stopRequested = true;
                baseFont.Dispose();
                bigFont.Dispose();
                rowHeadFont.Dispose();
                rowTextFont.Dispose();
            }
            base.Dispose(disposing);
        }
    }
}
