# Trimple UI: WinForms front end. Windows only. Dot-sourced by Trimple.ps1 (needs Trimple.Core imported).
# Keep this file ASCII-only and compatible with Windows PowerShell 5.1.
# All state lives in $script: variables so event handlers can reach it.

Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing

if (-not ('TrimpleNative' -as [type])) {
    Add-Type -ReferencedAssemblies System.Windows.Forms, System.Drawing -TypeDefinition @'
using System;
using System.Drawing;
using System.Runtime.InteropServices;
using System.Windows.Forms;

public static class TrimpleNative
{
    [DllImport("dwmapi.dll")]
    private static extern int DwmSetWindowAttribute(IntPtr hwnd, int attr, ref int value, int size);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)]
    private static extern IntPtr SendMessage(IntPtr hWnd, int msg, IntPtr wParam, string lParam);
    [DllImport("user32.dll")]
    private static extern bool SetProcessDPIAware();

    public static void SetDarkTitleBar(IntPtr hwnd, bool dark)
    {
        try
        {
            int v = dark ? 1 : 0;
            if (DwmSetWindowAttribute(hwnd, 20, ref v, 4) != 0) { DwmSetWindowAttribute(hwnd, 19, ref v, 4); }
        }
        catch (Exception) { }
    }
    public static void SetCue(IntPtr hwnd, string text)
    {
        try { SendMessage(hwnd, 0x1501, (IntPtr)1, text); } catch (Exception) { }
    }
    public static void EnableDpiAwareness()
    {
        try { SetProcessDPIAware(); } catch (Exception) { }
    }
}

public class TrimpleList : ListBox
{
    public TrimpleList()
    {
        this.DoubleBuffered = true;
        this.DrawMode = DrawMode.OwnerDrawFixed;
        this.IntegralHeight = false;
        this.BorderStyle = BorderStyle.None;
    }
}

public class TrimpleStrip : Panel
{
    public TrimpleStrip()
    {
        SetStyle(ControlStyles.OptimizedDoubleBuffer | ControlStyles.AllPaintingInWmPaint | ControlStyles.UserPaint | ControlStyles.ResizeRedraw, true);
    }
}

public class TrimpleColorTable : ProfessionalColorTable
{
    private Color _back, _hover, _border;
    public TrimpleColorTable(Color back, Color hover, Color border)
    {
        _back = back; _hover = hover; _border = border;
        this.UseSystemColors = false;
    }
    public override Color ToolStripDropDownBackground { get { return _back; } }
    public override Color MenuStripGradientBegin { get { return _back; } }
    public override Color MenuStripGradientEnd { get { return _back; } }
    public override Color MenuItemSelected { get { return _hover; } }
    public override Color MenuItemSelectedGradientBegin { get { return _hover; } }
    public override Color MenuItemSelectedGradientEnd { get { return _hover; } }
    public override Color MenuItemPressedGradientBegin { get { return _hover; } }
    public override Color MenuItemPressedGradientMiddle { get { return _hover; } }
    public override Color MenuItemPressedGradientEnd { get { return _hover; } }
    public override Color MenuItemBorder { get { return _hover; } }
    public override Color MenuBorder { get { return _border; } }
    public override Color ImageMarginGradientBegin { get { return _back; } }
    public override Color ImageMarginGradientMiddle { get { return _back; } }
    public override Color ImageMarginGradientEnd { get { return _back; } }
    public override Color SeparatorDark { get { return _border; } }
    public override Color SeparatorLight { get { return _border; } }
    public override Color CheckBackground { get { return _hover; } }
    public override Color CheckSelectedBackground { get { return _hover; } }
    public override Color CheckPressedBackground { get { return _hover; } }
    public override Color StatusStripGradientBegin { get { return _back; } }
    public override Color StatusStripGradientEnd { get { return _back; } }
}

public class TrimpleRenderer : ToolStripProfessionalRenderer
{
    private Color _fore, _disabled, _hover;
    public TrimpleRenderer(ProfessionalColorTable table, Color fore, Color disabled, Color hover) : base(table)
    {
        _fore = fore; _disabled = disabled; _hover = hover;
        this.RoundedEdges = false;
    }
    protected override void OnRenderItemText(ToolStripItemTextRenderEventArgs e)
    {
        e.TextColor = e.Item.Enabled ? _fore : _disabled;
        base.OnRenderItemText(e);
    }
    protected override void OnRenderArrow(ToolStripArrowRenderEventArgs e)
    {
        e.ArrowColor = _fore;
        base.OnRenderArrow(e);
    }
    protected override void OnRenderItemCheck(ToolStripItemImageRenderEventArgs e)
    {
        Rectangle r = e.ImageRectangle;
        using (SolidBrush b = new SolidBrush(_hover)) { e.Graphics.FillRectangle(b, r); }
        e.Graphics.SmoothingMode = System.Drawing.Drawing2D.SmoothingMode.AntiAlias;
        using (Pen p = new Pen(_fore, 2f))
        {
            e.Graphics.DrawLines(p, new Point[] {
                new Point(r.Left + 4, r.Top + r.Height / 2),
                new Point(r.Left + r.Width / 2 - 1, r.Bottom - 5),
                new Point(r.Right - 4, r.Top + 4) });
        }
    }
}
'@
}

[TrimpleNative]::EnableDpiAwareness()
[System.Windows.Forms.Application]::EnableVisualStyles()

# ------------------------------------------------------------------ state

$script:ui        = @{}
$script:Docs      = New-Object System.Collections.ArrayList
$script:ActiveDoc = $null
$script:Settings  = $null
$script:Index     = @{}
$script:AppRoot   = ''
$script:Folder    = ''
$script:Rows      = @()
$script:C         = @{}
$script:Fonts     = @{}
$script:Scale     = 1.0
$script:Suppress  = $false
$script:Busy      = $false
$script:Headless  = $false
$script:Untitled  = 0
$script:LastUiError = $null
$script:ThemeName = 'dark'

$script:ThemeDefs = @{
    dark = @{
        Back = '#1e1e1e'; Panel = '#252526'; Input = '#2d2d30'; Text = '#d4d4d4'; Muted = '#8a8a8a'
        Border = '#3c3c3c'; Hover = '#37373d'; Select = '#094771'; Accent = '#4ea1ff'
        P1 = '#f2645e'; P2 = '#e5a550'; P3 = '#5aa9e6'; PNone = '#9da5b4'; Done = '#6a6a6a'
        Warn = '#5a4a1e'; TabActive = '#1e1e1e'; TabIdle = '#2b2b2b'
    }
    light = @{
        Back = '#ffffff'; Panel = '#f3f3f3'; Input = '#ffffff'; Text = '#1f1f1f'; Muted = '#6e6e6e'
        Border = '#d0d0d0'; Hover = '#e4e6f1'; Select = '#cce4ff'; Accent = '#0b6bcb'
        P1 = '#d13438'; P2 = '#c27400'; P3 = '#0b6bcb'; PNone = '#6e6e6e'; Done = '#9a9a9a'
        Warn = '#fff4ce'; TabActive = '#ffffff'; TabIdle = '#e8e8e8'
    }
}

function S([int]$n) { return [int][Math]::Round($n * $script:Scale) }

function Write-TrimpleLog {
    param([string]$Message)
    try {
        $dir = $script:AppRoot
        if (-not $dir -or -not (Test-Path -LiteralPath $dir)) { $dir = [System.IO.Path]::GetTempPath() }
        $line = '{0:s}  {1}{2}' -f (Get-Date), $Message, [Environment]::NewLine
        [System.IO.File]::AppendAllText((Join-Path $dir 'Trimple.log'), $line)
    }
    catch { }
}

function Show-Message {
    param(
        [string]$Text,
        [string]$Title = 'Trimple',
        [string]$Buttons = 'OK',
        [string]$Icon = 'Information',
        [string]$HeadlessResult = 'No'
    )
    if ($script:Headless) { Write-TrimpleLog "message suppressed: $Text"; return $HeadlessResult }
    $owner = $null
    if ($script:ui.ContainsKey('Form')) { $owner = $script:ui.Form }
    $r = [System.Windows.Forms.MessageBox]::Show($owner, $Text, $Title, [System.Windows.Forms.MessageBoxButtons]$Buttons, [System.Windows.Forms.MessageBoxIcon]$Icon)
    return $r.ToString()
}

function Set-Status {
    param([string]$Message)
    if ($script:ui.ContainsKey('StatusMsg')) { $script:ui.StatusMsg.Text = $Message }
}

# ------------------------------------------------------------------ theme

function Set-ThemeColors {
    param([string]$Name)
    if (-not $script:ThemeDefs.ContainsKey($Name)) { $Name = 'dark' }
    $script:ThemeName = $Name
    $script:C = @{}
    foreach ($k in $script:ThemeDefs[$Name].Keys) {
        $script:C[$k] = [System.Drawing.ColorTranslator]::FromHtml($script:ThemeDefs[$Name][$k])
    }
}

function Initialize-Fonts {
    $family = 'Segoe UI'
    $script:Fonts.Ui     = New-Object System.Drawing.Font($family, 9.5)
    $script:Fonts.Small  = New-Object System.Drawing.Font($family, 8.5)
    $script:Fonts.Bold   = New-Object System.Drawing.Font($family, 8.5, [System.Drawing.FontStyle]::Bold)
    $script:Fonts.Strike = New-Object System.Drawing.Font($family, 9.5, [System.Drawing.FontStyle]::Strikeout)
}

function Get-EditorFont {
    $size = [single]$script:Settings.FontSize
    $installed = (New-Object System.Drawing.Text.InstalledFontCollection).Families | ForEach-Object { $_.Name }
    foreach ($name in @('Consolas', 'Cascadia Mono', 'Courier New')) {
        if ($installed -contains $name) { return (New-Object System.Drawing.Font($name, $size)) }
    }
    return (New-Object System.Drawing.Font([System.Drawing.FontFamily]::GenericMonospace, $size))
}

function Set-ControlTheme {
    param($Ctl)
    $c = $script:C
    if ($Ctl -is [System.Windows.Forms.TextBox]) {
        if ($Ctl.Multiline) { $Ctl.BackColor = $c.Back } else { $Ctl.BackColor = $c.Input }
        $Ctl.ForeColor = $c.Text
    }
    elseif ($Ctl -is [System.Windows.Forms.ComboBox]) {
        $Ctl.BackColor = $c.Input; $Ctl.ForeColor = $c.Text; $Ctl.FlatStyle = 'Flat'
    }
    elseif ($Ctl -is [System.Windows.Forms.Button]) {
        $Ctl.BackColor = $c.Input; $Ctl.ForeColor = $c.Text; $Ctl.FlatStyle = 'Flat'
        $Ctl.FlatAppearance.BorderColor = $c.Border
    }
    elseif ($Ctl -is [System.Windows.Forms.ListBox]) {
        $Ctl.BackColor = $c.Panel; $Ctl.ForeColor = $c.Text
    }
    elseif ($Ctl -is [System.Windows.Forms.SplitContainer]) {
        $Ctl.BackColor = $c.Border
        $Ctl.Panel1.BackColor = $c.Panel; $Ctl.Panel2.BackColor = $c.Panel
    }
    elseif ($Ctl -is [System.Windows.Forms.ToolStrip]) {
        $Ctl.BackColor = $c.Panel; $Ctl.ForeColor = $c.Text
    }
    else {
        $Ctl.BackColor = $c.Panel; $Ctl.ForeColor = $c.Text
    }
    foreach ($child in $Ctl.Controls) { Set-ControlTheme -Ctl $child }
}

function Apply-Theme {
    $c = $script:C
    $form = $script:ui.Form
    Set-ControlTheme -Ctl $form
    $form.BackColor = $c.Panel
    $script:ui.EditHost.BackColor = $c.Back
    $script:ui.Strip.BackColor = $c.Panel
    $script:ui.ConflictBar.BackColor = $c.Warn
    $script:ui.ConflictLabel.BackColor = $c.Warn
    $script:ui.ConflictLabel.ForeColor = $c.Text
    $script:ui.ConflictBar.ForeColor = $c.Text
    $script:ui.TaskList.BackColor = $c.Panel
    $script:ui.FileList.BackColor = $c.Panel
    foreach ($d in $script:Docs) { $d.Editor.BackColor = $c.Back; $d.Editor.ForeColor = $c.Text }
    $table = New-Object TrimpleColorTable($c.Panel, $c.Hover, $c.Border)
    $renderer = New-Object TrimpleRenderer($table, $c.Text, $c.Muted, $c.Hover)
    $script:ui.Menu.Renderer = $renderer
    $script:ui.StatusStrip.Renderer = $renderer
    $script:ui.Menu.BackColor = $c.Panel; $script:ui.Menu.ForeColor = $c.Text
    $script:ui.StatusStrip.BackColor = $c.Panel
    foreach ($item in $script:ui.StatusStrip.Items) { $item.ForeColor = $c.Muted }
    foreach ($item in $script:ui.Menu.Items) { Set-MenuItemColors -Item $item }
    $script:ui.TitleLabel.ForeColor = $c.Muted
    $script:ui.CountLabel.ForeColor = $c.Muted
    $script:ui.FilesTitle.ForeColor = $c.Muted
    if ($form.IsHandleCreated) { [TrimpleNative]::SetDarkTitleBar($form.Handle, ($script:ThemeName -eq 'dark')) }
    $script:ui.MiDark.Checked = ($script:ThemeName -eq 'dark')
    $script:ui.Strip.Invalidate()
    $script:ui.TaskList.Invalidate()
    $script:ui.FileList.Invalidate()
}

function Set-MenuItemColors {
    param($Item)
    $Item.ForeColor = $script:C.Text
    $Item.BackColor = $script:C.Panel
    if ($Item -is [System.Windows.Forms.ToolStripMenuItem]) {
        foreach ($sub in $Item.DropDownItems) { Set-MenuItemColors -Item $sub }
    }
}

function Set-Theme {
    param([string]$Name)
    Set-ThemeColors -Name $Name
    $script:Settings.Theme = $script:ThemeName
    Apply-Theme
}

# ------------------------------------------------------------------ helpers

function Get-FileStamp {
    param([string]$Path)
    try {
        $fi = New-Object System.IO.FileInfo($Path)
        if (-not $fi.Exists) { return '' }
        return ('{0}:{1}' -f $fi.LastWriteTimeUtc.Ticks, $fi.Length)
    }
    catch { return '' }
}

function Find-Doc {
    param([string]$Path)
    if (-not $Path) { return $null }
    foreach ($d in $script:Docs) {
        if ($d.Path -and ($d.Path -ieq $Path)) { return $d }
    }
    return $null
}

function Get-KindLabel {
    param([string]$Kind)
    switch ($Kind) {
        'utf8'    { return 'UTF-8' }
        'utf8bom' { return 'UTF-8 BOM' }
        'utf16le' { return 'UTF-16 LE' }
        'utf16be' { return 'UTF-16 BE' }
        'ansi'    { return 'ANSI' }
        default   { return $Kind }
    }
}

function Get-EolLabel {
    param([string]$Eol)
    if ($Eol -eq "`n") { return 'LF' }
    if ($Eol -eq "`r") { return 'CR' }
    return 'CRLF'
}

function ConvertTo-EditorText {
    param([string]$Text)
    return [regex]::Replace($Text, '\r\n|\n|\r', "`r`n")
}

function Get-LineOffset {
    param([string]$Text, [int]$Line)
    $offset = 0
    $current = 1
    while ($current -lt $Line) {
        $i = $Text.IndexOf("`n", $offset)
        if ($i -lt 0) { return $Text.Length }
        $offset = $i + 1
        $current++
    }
    return $offset
}

# ------------------------------------------------------------------ documents

function New-Doc {
    param([string]$Path = '', [string]$Text = '', [string]$Kind = 'utf8', [string]$Eol = "`r`n")
    $tb = New-Object System.Windows.Forms.TextBox
    $tb.Multiline = $true
    $tb.AcceptsTab = $true
    $tb.AcceptsReturn = $true
    $tb.WordWrap = [bool]$script:Settings.WordWrap
    $tb.ScrollBars = 'Both'
    $tb.BorderStyle = 'None'
    $tb.HideSelection = $false
    $tb.MaxLength = [int]::MaxValue
    $tb.Dock = 'Fill'
    $tb.Font = Get-EditorFont
    $tb.BackColor = $script:C.Back
    $tb.ForeColor = $script:C.Text
    $tb.Visible = $false

    $name = 'Untitled'
    if ($Path) { $name = [System.IO.Path]::GetFileName($Path) }
    else { $script:Untitled++; if ($script:Untitled -gt 1) { $name = "Untitled $($script:Untitled)" } }

    $doc = @{
        Path = $Path; Name = $name; Kind = $Kind; Eol = $Eol; Editor = $tb
        Dirty = $false; Stamp = ''; SeenStamp = ''; Conflict = ''; Loading = $true
        Rect = (New-Object System.Drawing.Rectangle(0, 0, 0, 0)); CloseRect = (New-Object System.Drawing.Rectangle(0, 0, 0, 0))
    }
    if ($Path) { $doc.Stamp = Get-FileStamp -Path $Path }
    $tb.Tag = $doc
    $tb.Text = ConvertTo-EditorText -Text $Text
    $doc.Loading = $false

    $tb.Add_TextChanged({
        param($s, $e)
        $d = $s.Tag
        if ($d.Loading) { return }
        if (-not $d.Dirty) { $d.Dirty = $true; Update-Chrome }
        Update-Position
    })
    $tb.Add_KeyDown({
        param($s, $e)
        if ($e.Control -and -not $e.Alt -and $e.KeyCode -eq [System.Windows.Forms.Keys]::A) { $s.SelectAll(); $e.SuppressKeyPress = $true }
    })
    $tb.Add_KeyUp({ param($s, $e) Update-Position })
    $tb.Add_MouseUp({ param($s, $e) Update-Position })

    $script:ui.EditHost.Controls.Add($tb)
    [void]$script:Docs.Add($doc)
    return $doc
}

function Show-Doc {
    param($Doc)
    if ($null -eq $Doc) { return }
    $script:ActiveDoc = $Doc
    foreach ($d in $script:Docs) { $d.Editor.Visible = ($d -eq $Doc) }
    $Doc.Editor.BringToFront()
    Update-Chrome
    Update-Position
    if (-not $script:Headless) { $Doc.Editor.Focus() | Out-Null }
}

function Open-Doc {
    param([string]$Path, [int]$Line = 0)
    $full = [System.IO.Path]::GetFullPath($Path)
    $doc = Find-Doc -Path $full
    if ($null -eq $doc) {
        try { $file = Read-TrimpleFile -Path $full }
        catch {
            [void](Show-Message -Text "Could not open '$full'.`n`n$($_.Exception.Message)" -Icon 'Warning')
            return $null
        }
        $doc = New-Doc -Path $full -Text $file.Text -Kind $file.Kind -Eol $file.Eol
        Remove-PristineUntitled -Except $doc
    }
    Show-Doc -Doc $doc
    if ($Line -gt 0) { Move-ToLine -Doc $doc -Line $Line }
    return $doc
}

function Move-ToLine {
    param($Doc, [int]$Line)
    $tb = $Doc.Editor
    $tb.SelectionStart = Get-LineOffset -Text $tb.Text -Line $Line
    $tb.SelectionLength = 0
    $tb.ScrollToCaret()
    Update-Position
}

# An empty, unsaved "Untitled" tab is replaced rather than left lying around.
function Remove-PristineUntitled {
    param($Except)
    foreach ($d in @($script:Docs)) {
        if ($d -ne $Except -and -not $d.Path -and -not $d.Dirty -and $d.Editor.TextLength -eq 0) {
            Remove-DocCore -Doc $d
        }
    }
}

function Remove-DocCore {
    param($Doc)
    $script:ui.EditHost.Controls.Remove($Doc.Editor)
    $Doc.Editor.Dispose()
    $script:Docs.Remove($Doc)
    if ($script:ActiveDoc -eq $Doc) { $script:ActiveDoc = $null }
}

function New-UntitledDoc {
    $doc = New-Doc
    Show-Doc -Doc $doc
    return $doc
}

function Confirm-SaveDoc {
    # Returns $true when it is OK to discard/close the doc.
    param($Doc)
    if (-not $Doc.Dirty) { return $true }
    Show-Doc -Doc $Doc
    $r = Show-Message -Text "Save changes to '$($Doc.Name)'?" -Buttons 'YesNoCancel' -Icon 'Question' -HeadlessResult 'Cancel'
    if ($r -eq 'Cancel') { return $false }
    if ($r -eq 'Yes') { return (Save-Doc -Doc $Doc) }
    return $true
}

function Close-Doc {
    param($Doc)
    if (-not (Confirm-SaveDoc -Doc $Doc)) { return $false }
    $idx = $script:Docs.IndexOf($Doc)
    Remove-DocCore -Doc $Doc
    if ($script:Docs.Count -eq 0) { [void](New-UntitledDoc) }
    else {
        $next = [Math]::Min($idx, $script:Docs.Count - 1)
        Show-Doc -Doc $script:Docs[$next]
    }
    return $true
}

function Read-DocFromDisk {
    param($Doc)
    $file = Read-TrimpleFile -Path $Doc.Path
    $tb = $Doc.Editor
    $caret = $tb.SelectionStart
    $Doc.Loading = $true
    $tb.Text = ConvertTo-EditorText -Text $file.Text
    $Doc.Loading = $false
    $Doc.Kind = $file.Kind
    $Doc.Eol = $file.Eol
    $Doc.Stamp = Get-FileStamp -Path $Doc.Path
    $Doc.SeenStamp = ''
    $Doc.Dirty = $false
    $Doc.Conflict = ''
    $tb.SelectionStart = [Math]::Min($caret, $tb.TextLength)
    $tb.SelectionLength = 0
    Update-Chrome
}

function Save-Doc {
    param($Doc, [bool]$SaveAs = $false)
    $path = $Doc.Path
    if ($SaveAs -or -not $path) {
        $dlg = New-Object System.Windows.Forms.SaveFileDialog
        $dlg.Filter = 'Text files (*.txt)|*.txt|All files (*.*)|*.*'
        $dlg.DefaultExt = 'txt'
        $dlg.OverwritePrompt = $true
        if ($path) { $dlg.InitialDirectory = Split-Path -Parent $path; $dlg.FileName = [System.IO.Path]::GetFileName($path) }
        else { if ($script:Folder -and (Test-Path -LiteralPath $script:Folder)) { $dlg.InitialDirectory = $script:Folder }; $dlg.FileName = 'Notes.txt' }
        $script:Busy = $true
        try { $r = $dlg.ShowDialog($script:ui.Form) } finally { $script:Busy = $false; $dlg.Dispose() }
        if ($r -ne [System.Windows.Forms.DialogResult]::OK) { return $false }
        $path = $dlg.FileName
    }
    elseif ($Doc.Path) {
        $disk = Get-FileStamp -Path $Doc.Path
        if ($disk -ne '' -and $disk -ne $Doc.Stamp) {
            $ans = Show-Message -Text "'$($Doc.Name)' was changed outside Trimple since you opened it.`n`nOverwrite it with your version?" -Buttons 'YesNo' -Icon 'Warning'
            if ($ans -ne 'Yes') { return $false }
        }
    }
    try {
        $text = [regex]::Replace($Doc.Editor.Text, '\r\n|\n|\r', $Doc.Eol)
        $enc = ConvertTo-TrimpleBytes -Text $text -Kind $Doc.Kind
        Write-TrimpleBytes -Path $path -Bytes $enc.Bytes
    }
    catch {
        [void](Show-Message -Text "Could not save '$path'.`n`n$($_.Exception.Message)" -Icon 'Error')
        return $false
    }
    $Doc.Path = $path
    $Doc.Name = [System.IO.Path]::GetFileName($path)
    $Doc.Kind = $enc.Kind
    $Doc.Stamp = Get-FileStamp -Path $path
    $Doc.SeenStamp = ''
    $Doc.Dirty = $false
    $Doc.Conflict = ''
    Update-Chrome
    Set-Status ("Saved " + $Doc.Name)
    [void](Update-Panels -Refresh)
    return $true
}

function Open-InNotepad {
    $doc = $script:ActiveDoc
    if ($null -eq $doc) { return }
    if (-not $doc.Path) {
        [void](Show-Message -Text 'Save this note first, then it can be opened in Notepad.')
        return
    }
    if ($doc.Dirty) {
        $r = Show-Message -Text "'$($doc.Name)' has unsaved changes.`n`nSave before opening in Notepad?" -Buttons 'YesNoCancel' -Icon 'Question' -HeadlessResult 'Cancel'
        if ($r -eq 'Cancel') { return }
        if ($r -eq 'Yes' -and -not (Save-Doc -Doc $doc)) { return }
    }
    try { Start-Process -FilePath 'notepad.exe' -ArgumentList ('"' + $doc.Path + '"') }
    catch { [void](Show-Message -Text "Could not start Notepad.`n`n$($_.Exception.Message)" -Icon 'Warning') }
}

# ------------------------------------------------------------------ editor commands

function Invoke-ToggleTaskOnLine {
    $doc = $script:ActiveDoc
    if ($null -eq $doc) { return }
    $tb = $doc.Editor
    $text = $tb.Text
    $pos = $tb.SelectionStart
    $start = 0
    if ($pos -gt 0) { $start = $text.LastIndexOf("`n", [Math]::Max($pos - 1, 0)) + 1 }
    $end = $text.IndexOf("`r", $start)
    if ($end -lt 0) { $end = $text.Length }
    $line = $text.Substring($start, $end - $start)
    $m = [regex]::Match($line, '^(?<pre>[ \t]*[-*+][ \t]+\[)(?<mark>[ xX])(?<post>\][ \t]+\S)')
    if (-not $m.Success) { Set-Status 'No task on this line.'; return }
    $g = $m.Groups['mark']
    $new = ' '
    if ($g.Value -eq ' ') { $new = 'x' }
    $tb.Select($start + $g.Index, 1)
    $tb.SelectedText = $new
    $tb.SelectionStart = [Math]::Min($pos, $tb.TextLength)
    $tb.SelectionLength = 0
}

function Invoke-InsertTask {
    $doc = $script:ActiveDoc
    if ($null -eq $doc) { return }
    $tb = $doc.Editor
    $text = $tb.Text
    $pos = $tb.SelectionStart
    $start = 0
    if ($pos -gt 0) { $start = $text.LastIndexOf("`n", [Math]::Max($pos - 1, 0)) + 1 }
    $tb.SelectionStart = $start
    $tb.SelectionLength = 0
    $tb.SelectedText = '- [ ] '
    $tb.Focus() | Out-Null
}

# ------------------------------------------------------------------ chrome (strip, title, status, conflict bar)

function Update-Chrome {
    $doc = $script:ActiveDoc
    if ($script:ui.ContainsKey('Strip')) { $script:ui.Strip.Invalidate() }
    if ($null -eq $doc) { return }
    $mark = ''
    if ($doc.Dirty) { $mark = '*' }
    $script:ui.Form.Text = "$mark$($doc.Name) - Trimple"
    if ($doc.Conflict) {
        $script:ui.ConflictBar.Visible = $true
        if ($doc.Conflict -eq 'deleted') {
            $script:ui.ConflictLabel.Text = "  '$($doc.Name)' was deleted or moved outside Trimple. Saving will recreate it."
            $script:ui.ReloadButton.Enabled = $false
        }
        else {
            $script:ui.ConflictLabel.Text = "  '$($doc.Name)' changed outside Trimple, and you have unsaved edits."
            $script:ui.ReloadButton.Enabled = $true
        }
    }
    else { $script:ui.ConflictBar.Visible = $false }
    $script:ui.StatusEnc.Text = Get-KindLabel -Kind $doc.Kind
    $script:ui.StatusEol.Text = Get-EolLabel -Eol $doc.Eol
}

function Update-Position {
    $doc = $script:ActiveDoc
    if ($null -eq $doc) { return }
    $tb = $doc.Editor
    $pos = [Math]::Min($tb.SelectionStart, $tb.TextLength)
    $before = $tb.Text.Substring(0, $pos)
    $lastBreak = $before.LastIndexOf("`n")
    $line = ($before.Length - $before.Replace("`n", '').Length) + 1
    $script:ui.StatusPos.Text = ('Ln {0}, Col {1}' -f $line, ($pos - $lastBreak))
}

function Paint-Strip {
    param($Sender, $E)
    try {
        $c = $script:C
        $g = $E.Graphics
        $g.Clear($c.Panel)
        $g.SmoothingMode = 'AntiAlias'
        $font = $script:Fonts.Ui
        $flags = [System.Windows.Forms.TextFormatFlags]'Left, VerticalCenter, EndEllipsis, NoPrefix, SingleLine'
        $count = $script:Docs.Count
        if ($count -eq 0) { return }
        $plus = S 30
        $avail = $Sender.ClientSize.Width - $plus - (S 4)
        $widths = @()
        $total = 0
        foreach ($d in $script:Docs) {
            $label = $d.Name
            $w = [System.Windows.Forms.TextRenderer]::MeasureText($g, $label, $font).Width + (S 46)
            $w = [Math]::Max((S 90), [Math]::Min((S 210), $w))
            $widths += $w; $total += $w
        }
        $factor = 1.0
        if ($total -gt $avail -and $total -gt 0) { $factor = [Math]::Max([double]$avail / $total, 0.4) }
        $x = 0
        $h = $Sender.ClientSize.Height
        for ($i = 0; $i -lt $count; $i++) {
            $d = $script:Docs[$i]
            $w = [int]($widths[$i] * $factor)
            $rect = New-Object System.Drawing.Rectangle($x, 0, ($w - 1), $h)
            $d.Rect = $rect
            $active = ($d -eq $script:ActiveDoc)
            $bg = $c.TabIdle
            if ($active) { $bg = $c.TabActive }
            $brush = New-Object System.Drawing.SolidBrush($bg)
            $g.FillRectangle($brush, $rect)
            $brush.Dispose()
            if ($active) {
                $accent = New-Object System.Drawing.SolidBrush($c.Accent)
                $g.FillRectangle($accent, $x, 0, ($w - 1), (S 2))
                $accent.Dispose()
            }
            $closeSize = S 16
            $closeRect = New-Object System.Drawing.Rectangle(($x + $w - 1 - $closeSize - (S 8)), (($h - $closeSize) / 2), $closeSize, $closeSize)
            $d.CloseRect = $closeRect
            $name = $d.Name
            if ($d.Conflict) { $name = '! ' + $name }
            $fore = $c.Muted
            if ($active) { $fore = $c.Text }
            $textRect = New-Object System.Drawing.Rectangle(($x + (S 10)), 0, ($w - (S 10) - $closeSize - (S 16)), $h)
            [System.Windows.Forms.TextRenderer]::DrawText($g, $name, $font, $textRect, $fore, $flags)
            if ($d.Dirty) {
                $dot = New-Object System.Drawing.SolidBrush($c.Text)
                $g.FillEllipse($dot, ($closeRect.Left + (S 4)), ($closeRect.Top + (S 4)), (S 8), (S 8))
                $dot.Dispose()
            }
            else {
                $pen = New-Object System.Drawing.Pen($c.Muted, [single]1.4)
                $pad = S 4
                $g.DrawLine($pen, ($closeRect.Left + $pad), ($closeRect.Top + $pad), ($closeRect.Right - $pad), ($closeRect.Bottom - $pad))
                $g.DrawLine($pen, ($closeRect.Right - $pad), ($closeRect.Top + $pad), ($closeRect.Left + $pad), ($closeRect.Bottom - $pad))
                $pen.Dispose()
            }
            $x += $w
        }
        $script:ui.PlusRect = New-Object System.Drawing.Rectangle($x, 0, $plus, $h)
        [System.Windows.Forms.TextRenderer]::DrawText($g, '+', $script:Fonts.Bold, $script:ui.PlusRect, $c.Muted, [System.Windows.Forms.TextFormatFlags]'HorizontalCenter, VerticalCenter, SingleLine')
        $line = New-Object System.Drawing.Pen($c.Border)
        $g.DrawLine($line, 0, ($h - 1), $Sender.ClientSize.Width, ($h - 1))
        $line.Dispose()
    }
    catch { $script:LastUiError = $_.Exception.ToString(); Write-TrimpleLog $script:LastUiError }
}

function Invoke-StripMouse {
    param($E)
    $pt = $E.Location
    foreach ($d in @($script:Docs)) {
        if ($d.Rect.Contains($pt)) {
            if ($E.Button -eq [System.Windows.Forms.MouseButtons]::Middle -or $d.CloseRect.Contains($pt)) { [void](Close-Doc -Doc $d) }
            else { Show-Doc -Doc $d }
            return
        }
    }
    if ($script:ui.ContainsKey('PlusRect') -and $script:ui.PlusRect.Contains($pt)) { [void](New-UntitledDoc) }
}

function Switch-Doc {
    param([int]$Step)
    if ($script:Docs.Count -lt 2) { return }
    $i = $script:Docs.IndexOf($script:ActiveDoc)
    $n = ($i + $Step + $script:Docs.Count) % $script:Docs.Count
    Show-Doc -Doc $script:Docs[$n]
}

# ------------------------------------------------------------------ task panel

function Get-RowKey {
    param($Row)
    if ($null -ne $Row -and $Row.Kind -eq 'task') { return ('{0}|{1}' -f $Row.Task.File, $Row.Task.Line) }
    return ''
}

function Update-Panels {
    param([switch]$Refresh)
    if ($Refresh -and $script:Folder) {
        $result = Update-TrimpleTaskIndex -Index $script:Index -Root $script:Folder
        if ($result.Errors.Count -gt 0) { Set-Status ("Skipped unreadable file: " + $result.Errors[0]) }
    }
    $all = @(Get-TrimpleIndexTasks -Index $script:Index)
    $script:Suppress = $true
    try {
        # label filter
        $combo = $script:ui.LabelCombo
        $previous = ''
        if ($combo.SelectedIndex -gt 0) { $previous = [string]$combo.SelectedItem }
        $combo.Items.Clear()
        [void]$combo.Items.Add('All labels')
        foreach ($l in (Get-TrimpleLabels -Tasks $all)) { [void]$combo.Items.Add($l) }
        $combo.SelectedIndex = 0
        if ($previous) { $idx = $combo.Items.IndexOf($previous); if ($idx -gt 0) { $combo.SelectedIndex = $idx } }
    }
    finally { $script:Suppress = $false }

    $label = ''
    if ($script:ui.LabelCombo.SelectedIndex -gt 0) { $label = [string]$script:ui.LabelCombo.SelectedItem }
    $by = [string]$script:ui.GroupCombo.SelectedItem
    if (-not $by) { $by = 'Priority' }
    $showDone = $script:ui.ShowDone.Checked

    $selected = @(Select-TrimpleTasks -Tasks $all -Label $label -Search $script:ui.Search.Text -IncludeDone $showDone)
    $groups = @(Group-TrimpleTasks -Tasks $selected -By $by)

    $lb = $script:ui.TaskList
    $keepKey = Get-RowKey -Row $lb.SelectedItem
    $top = $lb.TopIndex
    $rows = New-Object System.Collections.Generic.List[object]
    foreach ($g in $groups) {
        $rows.Add(@{ Kind = 'header'; Text = ('{0}   {1}' -f $g.Name.ToUpperInvariant(), $g.OpenCount) })
        foreach ($t in $g.Tasks) { $rows.Add(@{ Kind = 'task'; Task = $t }) }
    }
    $script:Rows = $rows.ToArray()
    $lb.BeginUpdate()
    try {
        $lb.Items.Clear()
        foreach ($r in $script:Rows) { [void]$lb.Items.Add($r) }
        if ($keepKey) {
            for ($i = 0; $i -lt $lb.Items.Count; $i++) { if ((Get-RowKey -Row $lb.Items[$i]) -eq $keepKey) { $lb.SelectedIndex = $i; break } }
        }
        if ($top -lt $lb.Items.Count) { $lb.TopIndex = $top }
    }
    finally { $lb.EndUpdate() }

    $counts = Get-TrimpleOpenCounts -Tasks $all
    $script:ui.CountLabel.Text = ('{0} open  |  {1} high  {2} med  {3} low' -f $counts.Total, $counts.High, $counts.Medium, $counts.Low)

    # file list
    $fl = $script:ui.FileList
    $fl.BeginUpdate()
    try {
        $fl.Items.Clear()
        foreach ($f in @(Get-TrimpleIndexFiles -Index $script:Index)) { [void]$fl.Items.Add($f) }
    }
    finally { $fl.EndUpdate() }
    $script:ui.FilesTitle.Text = ('FILES   {0}' -f $fl.Items.Count)
    return $groups.Count
}

function Draw-TaskRow {
    param($Sender, $E)
    try {
        if ($E.Index -lt 0 -or $E.Index -ge $Sender.Items.Count) { return }
        $row = $Sender.Items[$E.Index]
        $c = $script:C
        $g = $E.Graphics
        $b = $E.Bounds
        $selected = ($Sender.SelectedIndex -eq $E.Index)
        $bgColor = $c.Panel
        if ($selected -and $row.Kind -eq 'task') { $bgColor = $c.Select }
        $brush = New-Object System.Drawing.SolidBrush($bgColor)
        $g.FillRectangle($brush, $b)
        $brush.Dispose()
        $flags = [System.Windows.Forms.TextFormatFlags]'Left, VerticalCenter, EndEllipsis, NoPrefix, SingleLine'

        if ($row.Kind -eq 'header') {
            $r = New-Object System.Drawing.Rectangle(($b.Left + (S 10)), ($b.Top + (S 4)), ($b.Width - (S 20)), ($b.Height - (S 4)))
            [System.Windows.Forms.TextRenderer]::DrawText($g, $row.Text, $script:Fonts.Bold, $r, $c.Muted, $flags)
            return
        }

        $t = $row.Task
        $g.SmoothingMode = 'AntiAlias'
        $pColor = $c.PNone
        if ($t.Priority -eq 1) { $pColor = $c.P1 } elseif ($t.Priority -eq 2) { $pColor = $c.P2 } elseif ($t.Priority -eq 3) { $pColor = $c.P3 }
        if (-not $t.Done -and $t.Priority -gt 0) {
            $stripe = New-Object System.Drawing.SolidBrush($pColor)
            $g.FillRectangle($stripe, $b.Left, ($b.Top + 2), (S 3), ($b.Height - 4))
            $stripe.Dispose()
        }
        $box = S 14
        $boxRect = New-Object System.Drawing.Rectangle(($b.Left + (S 12)), ($b.Top + (($b.Height - $box) / 2)), $box, $box)
        if ($t.Done) {
            $fill = New-Object System.Drawing.SolidBrush($c.Done)
            $g.FillRectangle($fill, $boxRect)
            $fill.Dispose()
            $tick = New-Object System.Drawing.Pen($c.Panel, [single]2)
            $g.DrawLines($tick, @(
                (New-Object System.Drawing.Point(($boxRect.Left + (S 3)), ($boxRect.Top + [int]($box / 2)))),
                (New-Object System.Drawing.Point(($boxRect.Left + [int]($box / 2) - 1), ($boxRect.Bottom - (S 4)))),
                (New-Object System.Drawing.Point(($boxRect.Right - (S 3)), ($boxRect.Top + (S 3))))))
            $tick.Dispose()
        }
        else {
            $pen = New-Object System.Drawing.Pen($pColor, [single]1.6)
            $g.DrawRectangle($pen, $boxRect)
            $pen.Dispose()
        }

        $labelsText = ''
        if (@($t.Labels).Count -gt 0) { $labelsText = (($t.Labels | ForEach-Object { '#' + $_ }) -join ' ') }
        $lw = 0
        if ($labelsText) { $lw = [System.Windows.Forms.TextRenderer]::MeasureText($g, $labelsText, $script:Fonts.Small).Width + (S 8) }
        $textLeft = $b.Left + (S 36)
        $textRect = New-Object System.Drawing.Rectangle($textLeft, $b.Top, [Math]::Max(($b.Width - $textLeft - $lw - (S 8)), 20), $b.Height)
        $font = $script:Fonts.Ui
        $fore = $c.Text
        if ($t.Done) { $font = $script:Fonts.Strike; $fore = $c.Done }
        [System.Windows.Forms.TextRenderer]::DrawText($g, $t.Text, $font, $textRect, $fore, $flags)
        if ($labelsText) {
            $lr = New-Object System.Drawing.Rectangle(($b.Right - $lw - (S 6)), $b.Top, $lw, $b.Height)
            [System.Windows.Forms.TextRenderer]::DrawText($g, $labelsText, $script:Fonts.Small, $lr, $c.Accent, [System.Windows.Forms.TextFormatFlags]'Right, VerticalCenter, NoPrefix, SingleLine')
        }
    }
    catch { $script:LastUiError = $_.Exception.ToString(); Write-TrimpleLog $script:LastUiError }
}

function Draw-FileRow {
    param($Sender, $E)
    try {
        if ($E.Index -lt 0 -or $E.Index -ge $Sender.Items.Count) { return }
        $f = $Sender.Items[$E.Index]
        $c = $script:C
        $bgColor = $c.Panel
        if ($Sender.SelectedIndex -eq $E.Index) { $bgColor = $c.Select }
        $brush = New-Object System.Drawing.SolidBrush($bgColor)
        $E.Graphics.FillRectangle($brush, $E.Bounds)
        $brush.Dispose()
        $flags = [System.Windows.Forms.TextFormatFlags]'Left, VerticalCenter, EndEllipsis, NoPrefix, SingleLine'
        $b = $E.Bounds
        $countText = ''
        if ($f.OpenTasks -gt 0) { $countText = [string]$f.OpenTasks }
        $cw = 0
        if ($countText) { $cw = [System.Windows.Forms.TextRenderer]::MeasureText($E.Graphics, $countText, $script:Fonts.Small).Width + (S 10) }
        $r = New-Object System.Drawing.Rectangle(($b.Left + (S 10)), $b.Top, [Math]::Max(($b.Width - (S 20) - $cw), 20), $b.Height)
        [System.Windows.Forms.TextRenderer]::DrawText($E.Graphics, $f.RelPath, $script:Fonts.Ui, $r, $c.Text, $flags)
        if ($countText) {
            $cr = New-Object System.Drawing.Rectangle(($b.Right - $cw - (S 6)), $b.Top, $cw, $b.Height)
            [System.Windows.Forms.TextRenderer]::DrawText($E.Graphics, $countText, $script:Fonts.Small, $cr, $c.Muted, [System.Windows.Forms.TextFormatFlags]'Right, VerticalCenter, NoPrefix, SingleLine')
        }
    }
    catch { $script:LastUiError = $_.Exception.ToString(); Write-TrimpleLog $script:LastUiError }
}

function Switch-TaskState {
    param($Task)
    $doc = Find-Doc -Path $Task.File
    if ($null -ne $doc -and $doc.Dirty) {
        Set-Status ("Save '" + $doc.Name + "' first - it has unsaved changes.")
        return
    }
    try {
        Set-TrimpleTaskState -Path $Task.File -Line $Task.Line -Done (-not $Task.Done) -ExpectedRaw $Task.Raw
    }
    catch {
        Set-Status ("Could not update task: " + $_.Exception.Message)
        [void]$script:Index.Remove($Task.File.ToLowerInvariant())
        [void](Update-Panels -Refresh)
        return
    }
    # Force a re-parse: coarse file-system timestamps could otherwise hide a same-size edit.
    [void]$script:Index.Remove($Task.File.ToLowerInvariant())
    [void](Update-Panels -Refresh)
    if ($null -ne $doc) { Read-DocFromDisk -Doc $doc }
}

function Invoke-TaskClick {
    param($E)
    $lb = $script:ui.TaskList
    $i = $lb.IndexFromPoint($E.Location)
    if ($i -lt 0) { return }
    $row = $lb.Items[$i]
    if ($row.Kind -ne 'task') { return }
    $lb.SelectedIndex = $i
    if ($E.X -lt (S 34)) { Switch-TaskState -Task $row.Task }
    else {
        $doc = Open-Doc -Path $row.Task.File -Line $row.Task.Line
        if ($null -ne $doc) { Set-Status ("{0}, line {1}" -f $row.Task.RelPath, $row.Task.Line) }
    }
}

# ------------------------------------------------------------------ folder + polling

function Set-NotesFolder {
    param([string]$Path)
    $script:Folder = $Path
    $script:Settings.Folder = $Path
    $script:Index = @{}
    $script:ui.FolderLabel.Text = $Path
    [void](Update-Panels -Refresh)
    Set-Status ("Notes folder: " + $Path)
}

function Select-NotesFolder {
    $dlg = New-Object System.Windows.Forms.FolderBrowserDialog
    $dlg.Description = 'Choose the folder that holds your notes (.txt files).'
    if ($script:Folder -and (Test-Path -LiteralPath $script:Folder)) { $dlg.SelectedPath = $script:Folder }
    $script:Busy = $true
    try { $r = $dlg.ShowDialog($script:ui.Form) } finally { $script:Busy = $false }
    $picked = $dlg.SelectedPath
    $dlg.Dispose()
    if ($r -eq [System.Windows.Forms.DialogResult]::OK -and $picked) { Set-NotesFolder -Path $picked }
}

function Get-WelcomeText {
    return (@(
        '# Welcome to Trimple',
        '',
        'This is a plain text file. Notepad can open it too.',
        'Any line that looks like a checkbox becomes a task in the panel on the left:',
        '',
        '- [ ] Write the weekly report !1 #work',
        '- [ ] Book dentist !3 #home',
        '- [x] Install Trimple #admin',
        '',
        '  [ ] / [x]   open / done        click the box in the panel to tick it',
        '  !1 !2 !3    priority (1 = high)',
        '  #word       label, as many as you like',
        ''
    ) -join "`r`n")
}

function Initialize-NotesFolder {
    param([string]$Override)
    if ($Override) { return $Override }
    $saved = [string]$script:Settings.Folder
    if ($saved -and (Test-Path -LiteralPath $saved -PathType Container)) { return $saved }
    $default = Join-Path ([Environment]::GetFolderPath('MyDocuments')) 'Trimple Notes'
    $dlg = New-Object System.Windows.Forms.FolderBrowserDialog
    $dlg.Description = "Welcome to Trimple. Choose the folder that holds your notes (.txt files).`nCancel to use '$default'."
    $r = $dlg.ShowDialog()
    $picked = $dlg.SelectedPath
    $dlg.Dispose()
    if ($r -eq [System.Windows.Forms.DialogResult]::OK -and $picked) { return $picked }
    if (-not (Test-Path -LiteralPath $default)) { New-Item -ItemType Directory -Path $default -Force | Out-Null }
    if (@(Get-TrimpleFileList -Root $default).Count -eq 0) {
        $enc = New-Object System.Text.UTF8Encoding($false)
        [System.IO.File]::WriteAllText((Join-Path $default 'Welcome.txt'), (Get-WelcomeText), $enc)
    }
    return $default
}

function Invoke-Poll {
    if ($script:Busy) { return }
    foreach ($doc in @($script:Docs)) {
        if (-not $doc.Path) { continue }
        $stamp = Get-FileStamp -Path $doc.Path
        if ($stamp -eq $doc.Stamp -or $stamp -eq $doc.SeenStamp) { continue }
        if ($stamp -eq '') {
            if ($doc.Conflict -ne 'deleted') { $doc.Conflict = 'deleted'; $doc.SeenStamp = $stamp }
            continue
        }
        if (-not $doc.Dirty) {
            try { Read-DocFromDisk -Doc $doc; Set-Status ("Reloaded " + $doc.Name + " (changed outside Trimple)") }
            catch { $doc.SeenStamp = $stamp }
        }
        else { $doc.Conflict = 'changed'; $doc.SeenStamp = $stamp }
    }
    Update-Chrome
    if ($script:Folder) {
        $r = Update-TrimpleTaskIndex -Index $script:Index -Root $script:Folder
        if ($r.Changed.Count -gt 0 -or $r.Removed.Count -gt 0) { [void](Update-Panels) }
    }
}

# ------------------------------------------------------------------ menu / form construction

function Add-MenuItem {
    param($Parent, [string]$Text, [string]$Keys = '', [scriptblock]$Click = $null, [bool]$CheckOnClick = $false)
    if ($Text -eq '-') { [void]$Parent.DropDownItems.Add((New-Object System.Windows.Forms.ToolStripSeparator)); return $null }
    $mi = New-Object System.Windows.Forms.ToolStripMenuItem($Text)
    if ($Keys) { try { $mi.ShortcutKeys = [System.Enum]::Parse([System.Windows.Forms.Keys], $Keys) } catch { } }
    $mi.CheckOnClick = $CheckOnClick
    if ($null -ne $Click) { $mi.Add_Click($Click) }
    [void]$Parent.DropDownItems.Add($mi)
    return $mi
}

function Set-FontSize {
    param([int]$Size)
    $Size = [Math]::Max(8, [Math]::Min(32, $Size))
    $script:Settings.FontSize = $Size
    foreach ($d in $script:Docs) { $old = $d.Editor.Font; $d.Editor.Font = Get-EditorFont; $old.Dispose() }
}

function Set-TaskPanelVisible {
    param([bool]$Visible)
    $script:Settings.TaskPanelVisible = $Visible
    $script:ui.Split.Panel1Collapsed = -not $Visible
    $script:ui.MiPanel.Checked = $Visible
}

function New-TrimpleUI {
    $form = New-Object System.Windows.Forms.Form
    $script:ui.Form = $form
    $form.Text = 'Trimple'
    $form.Font = $script:Fonts.Ui
    $form.KeyPreview = $true
    $form.MinimumSize = New-Object System.Drawing.Size((S 720), (S 420))
    $form.StartPosition = 'CenterScreen'
    $w = $script:Settings.Window
    $form.Size = New-Object System.Drawing.Size([int]$w.Width, [int]$w.Height)

    # --- left: tasks + files
    $split = New-Object System.Windows.Forms.SplitContainer
    $split.Dock = 'Fill'
    $split.SplitterWidth = 3
    $split.FixedPanel = 'Panel1'
    $split.Panel1MinSize = S 220
    $split.Panel2MinSize = S 300
    $script:ui.Split = $split

    $left = New-Object System.Windows.Forms.SplitContainer
    $left.Dock = 'Fill'
    $left.Orientation = 'Horizontal'
    $left.SplitterWidth = 3
    $left.Panel1MinSize = S 120
    $left.Panel2MinSize = S 60
    $script:ui.LeftSplit = $left
    $split.Panel1.Controls.Add($left)

    # task header (table layout keeps it resizable)
    $hdr = New-Object System.Windows.Forms.TableLayoutPanel
    $hdr.Dock = 'Top'
    $hdr.Height = S 122
    $hdr.ColumnCount = 2
    [void]$hdr.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle('Absolute', (S 112))))
    [void]$hdr.ColumnStyles.Add((New-Object System.Windows.Forms.ColumnStyle('Percent', 100)))
    $hdr.RowCount = 4
    foreach ($h in @(28, 30, 30, 26)) { [void]$hdr.RowStyles.Add((New-Object System.Windows.Forms.RowStyle('Absolute', (S $h)))) }

    $title = New-Object System.Windows.Forms.Label
    $title.Text = 'TASKS'; $title.Font = $script:Fonts.Bold; $title.Dock = 'Fill'; $title.TextAlign = 'MiddleLeft'
    $title.Padding = New-Object System.Windows.Forms.Padding((S 6), 0, 0, 0)
    $script:ui.TitleLabel = $title
    $count = New-Object System.Windows.Forms.Label
    $count.Text = ''; $count.Font = $script:Fonts.Small; $count.Dock = 'Fill'; $count.TextAlign = 'MiddleRight'
    $script:ui.CountLabel = $count
    $hdr.Controls.Add($title, 0, 0); $hdr.Controls.Add($count, 1, 0)

    $group = New-Object System.Windows.Forms.ComboBox
    $group.DropDownStyle = 'DropDownList'; $group.Dock = 'Fill'; $group.Margin = New-Object System.Windows.Forms.Padding(4, 2, 4, 2)
    foreach ($g in @('Priority', 'Label', 'File')) { [void]$group.Items.Add($g) }
    $group.SelectedItem = [string]$script:Settings.GroupBy
    if ($group.SelectedIndex -lt 0) { $group.SelectedIndex = 0 }
    $script:ui.GroupCombo = $group
    $labelCombo = New-Object System.Windows.Forms.ComboBox
    $labelCombo.DropDownStyle = 'DropDownList'; $labelCombo.Dock = 'Fill'; $labelCombo.Margin = New-Object System.Windows.Forms.Padding(4, 2, 4, 2)
    [void]$labelCombo.Items.Add('All labels'); $labelCombo.SelectedIndex = 0
    $script:ui.LabelCombo = $labelCombo
    $hdr.Controls.Add($group, 0, 1); $hdr.Controls.Add($labelCombo, 1, 1)

    $search = New-Object System.Windows.Forms.TextBox
    $search.Dock = 'Fill'; $search.BorderStyle = 'FixedSingle'; $search.Margin = New-Object System.Windows.Forms.Padding(4, 3, 4, 3)
    $script:ui.Search = $search
    $hdr.Controls.Add($search, 0, 2); $hdr.SetColumnSpan($search, 2)

    $showDone = New-Object System.Windows.Forms.CheckBox
    $showDone.Text = 'Show completed'; $showDone.Dock = 'Fill'; $showDone.Checked = [bool]$script:Settings.ShowDone
    $showDone.Padding = New-Object System.Windows.Forms.Padding((S 4), 0, 0, 0)
    $script:ui.ShowDone = $showDone
    $hdr.Controls.Add($showDone, 0, 3); $hdr.SetColumnSpan($showDone, 2)

    $taskList = New-Object TrimpleList
    $taskList.Dock = 'Fill'
    $taskList.ItemHeight = S 28
    $script:ui.TaskList = $taskList
    $left.Panel1.Controls.Add($taskList)
    $left.Panel1.Controls.Add($hdr)

    # file list
    $fileList = New-Object TrimpleList
    $fileList.Dock = 'Fill'
    $fileList.ItemHeight = S 24
    $script:ui.FileList = $fileList
    $filesTitle = New-Object System.Windows.Forms.Label
    $filesTitle.Text = 'FILES'; $filesTitle.Font = $script:Fonts.Bold; $filesTitle.Dock = 'Top'; $filesTitle.Height = S 24; $filesTitle.TextAlign = 'MiddleLeft'
    $filesTitle.Padding = New-Object System.Windows.Forms.Padding((S 6), 0, 0, 0)
    $script:ui.FilesTitle = $filesTitle
    $folderLabel = New-Object System.Windows.Forms.Label
    $folderLabel.Text = ''; $folderLabel.Font = $script:Fonts.Small; $folderLabel.Dock = 'Bottom'; $folderLabel.Height = S 22; $folderLabel.TextAlign = 'MiddleLeft'
    $folderLabel.AutoEllipsis = $true
    $folderLabel.Padding = New-Object System.Windows.Forms.Padding((S 6), 0, 0, 0)
    $script:ui.FolderLabel = $folderLabel
    $left.Panel2.Controls.Add($fileList)
    $left.Panel2.Controls.Add($filesTitle)
    $left.Panel2.Controls.Add($folderLabel)

    # --- right: tabs + editor
    $editHost = New-Object System.Windows.Forms.Panel
    $editHost.Dock = 'Fill'
    $script:ui.EditHost = $editHost
    $conflict = New-Object System.Windows.Forms.Panel
    $conflict.Dock = 'Top'; $conflict.Height = S 32; $conflict.Visible = $false
    $script:ui.ConflictBar = $conflict
    $keepBtn = New-Object System.Windows.Forms.Button
    $keepBtn.Text = 'Keep mine'; $keepBtn.Dock = 'Right'; $keepBtn.Width = S 90
    $reloadBtn = New-Object System.Windows.Forms.Button
    $reloadBtn.Text = 'Reload from disk'; $reloadBtn.Dock = 'Right'; $reloadBtn.Width = S 120
    $script:ui.ReloadButton = $reloadBtn
    $cLabel = New-Object System.Windows.Forms.Label
    $cLabel.Dock = 'Fill'; $cLabel.TextAlign = 'MiddleLeft'
    $script:ui.ConflictLabel = $cLabel
    $conflict.Controls.Add($cLabel)
    $conflict.Controls.Add($reloadBtn)
    $conflict.Controls.Add($keepBtn)
    $strip = New-Object TrimpleStrip
    $strip.Dock = 'Top'; $strip.Height = S 34
    $script:ui.Strip = $strip
    $split.Panel2.Controls.Add($editHost)
    $split.Panel2.Controls.Add($conflict)
    $split.Panel2.Controls.Add($strip)

    # --- menu + status
    $menu = New-Object System.Windows.Forms.MenuStrip
    $script:ui.Menu = $menu
    $mFile = New-Object System.Windows.Forms.ToolStripMenuItem('&File')
    $mEdit = New-Object System.Windows.Forms.ToolStripMenuItem('&Edit')
    $mView = New-Object System.Windows.Forms.ToolStripMenuItem('&View')
    $mHelp = New-Object System.Windows.Forms.ToolStripMenuItem('&Help')
    [void]$menu.Items.Add($mFile); [void]$menu.Items.Add($mEdit); [void]$menu.Items.Add($mView); [void]$menu.Items.Add($mHelp)

    [void](Add-MenuItem $mFile '&New tab' 'Control, N' { [void](New-UntitledDoc) })
    [void](Add-MenuItem $mFile '&Open...' 'Control, O' { Invoke-OpenDialog })
    [void](Add-MenuItem $mFile 'Open &folder...' 'Control, Shift, O' { Select-NotesFolder })
    [void](Add-MenuItem $mFile '-')
    [void](Add-MenuItem $mFile '&Save' 'Control, S' { if ($script:ActiveDoc) { [void](Save-Doc -Doc $script:ActiveDoc) } })
    [void](Add-MenuItem $mFile 'Save &as...' 'Control, Shift, S' { if ($script:ActiveDoc) { [void](Save-Doc -Doc $script:ActiveDoc -SaveAs $true) } })
    [void](Add-MenuItem $mFile '&Close tab' 'Control, W' { if ($script:ActiveDoc) { [void](Close-Doc -Doc $script:ActiveDoc) } })
    [void](Add-MenuItem $mFile '-')
    [void](Add-MenuItem $mFile 'Open in &Notepad' 'Control, Shift, N' { Open-InNotepad })
    [void](Add-MenuItem $mFile '-')
    [void](Add-MenuItem $mFile 'E&xit' '' { $script:ui.Form.Close() })

    [void](Add-MenuItem $mEdit '&Insert task' 'Control, Shift, T' { Invoke-InsertTask })
    [void](Add-MenuItem $mEdit '&Tick / untick task on this line' 'Control, Return' { Invoke-ToggleTaskOnLine })

    $script:ui.MiPanel = Add-MenuItem $mView '&Task panel' 'Control, B' { Set-TaskPanelVisible -Visible $script:ui.MiPanel.Checked } $true
    $script:ui.MiPanel.Checked = [bool]$script:Settings.TaskPanelVisible
    $script:ui.MiWrap = Add-MenuItem $mView '&Word wrap' '' { $script:Settings.WordWrap = $script:ui.MiWrap.Checked; foreach ($d in $script:Docs) { $d.Editor.WordWrap = $script:ui.MiWrap.Checked } } $true
    $script:ui.MiWrap.Checked = [bool]$script:Settings.WordWrap
    $script:ui.MiDark = Add-MenuItem $mView '&Dark theme' 'Control, Shift, D' { if ($script:ThemeName -eq 'dark') { Set-Theme -Name 'light' } else { Set-Theme -Name 'dark' } }
    [void](Add-MenuItem $mView '-')
    [void](Add-MenuItem $mView 'Font &larger' 'Control, Oemplus' { Set-FontSize -Size ($script:Settings.FontSize + 1) })
    [void](Add-MenuItem $mView 'Font &smaller' 'Control, OemMinus' { Set-FontSize -Size ($script:Settings.FontSize - 1) })
    [void](Add-MenuItem $mView '&Reset font size' 'Control, D0' { Set-FontSize -Size 11 })
    [void](Add-MenuItem $mView '-')
    [void](Add-MenuItem $mView '&Next tab' 'Control, Tab' { Switch-Doc -Step 1 })
    [void](Add-MenuItem $mView '&Previous tab' 'Control, Shift, Tab' { Switch-Doc -Step -1 })
    [void](Add-MenuItem $mView 'Re&fresh tasks' 'F5' { [void](Update-Panels -Refresh) })

    [void](Add-MenuItem $mHelp '&Task syntax' '' { [void](Show-Message -Text (Get-WelcomeText)) })

    $status = New-Object System.Windows.Forms.StatusStrip
    $status.SizingGrip = $false
    $script:ui.StatusStrip = $status
    $msg = New-Object System.Windows.Forms.ToolStripStatusLabel
    $msg.Spring = $true; $msg.TextAlign = 'MiddleLeft'
    $pos = New-Object System.Windows.Forms.ToolStripStatusLabel; $pos.AutoSize = $false; $pos.Width = S 110
    $enc = New-Object System.Windows.Forms.ToolStripStatusLabel; $enc.AutoSize = $false; $enc.Width = S 80
    $eol = New-Object System.Windows.Forms.ToolStripStatusLabel; $eol.AutoSize = $false; $eol.Width = S 50
    [void]$status.Items.Add($msg); [void]$status.Items.Add($pos); [void]$status.Items.Add($enc); [void]$status.Items.Add($eol)
    $script:ui.StatusMsg = $msg; $script:ui.StatusPos = $pos; $script:ui.StatusEnc = $enc; $script:ui.StatusEol = $eol

    $form.Controls.Add($split)
    $form.Controls.Add($status)
    $form.Controls.Add($menu)
    $form.MainMenuStrip = $menu

    # --- events
    $strip.Add_Paint({ param($s, $e) Paint-Strip -Sender $s -E $e })
    $strip.Add_MouseDown({ param($s, $e) Invoke-StripMouse -E $e })
    $strip.Add_Resize({ $script:ui.Strip.Invalidate() })
    $taskList.Add_DrawItem({ param($s, $e) Draw-TaskRow -Sender $s -E $e })
    $fileList.Add_DrawItem({ param($s, $e) Draw-FileRow -Sender $s -E $e })
    $taskList.Add_MouseDown({ param($s, $e) Invoke-TaskClick -E $e })
    $taskList.Add_KeyDown({
        param($s, $e)
        $row = $s.SelectedItem
        if ($null -eq $row -or $row.Kind -ne 'task') { return }
        if ($e.KeyCode -eq [System.Windows.Forms.Keys]::Space) { Switch-TaskState -Task $row.Task; $e.Handled = $true; $e.SuppressKeyPress = $true }
        elseif ($e.KeyCode -eq [System.Windows.Forms.Keys]::Return) { [void](Open-Doc -Path $row.Task.File -Line $row.Task.Line); $e.Handled = $true; $e.SuppressKeyPress = $true }
    })
    $fileList.Add_MouseDown({
        param($s, $e)
        $i = $s.IndexFromPoint($e.Location)
        if ($i -ge 0) { $s.SelectedIndex = $i; [void](Open-Doc -Path $s.Items[$i].Path) }
    })
    $group.Add_SelectedIndexChanged({ if (-not $script:Suppress) { $script:Settings.GroupBy = [string]$script:ui.GroupCombo.SelectedItem; [void](Update-Panels) } })
    $labelCombo.Add_SelectedIndexChanged({ if (-not $script:Suppress) { [void](Update-Panels) } })
    $search.Add_TextChanged({ [void](Update-Panels) })
    $showDone.Add_CheckedChanged({ $script:Settings.ShowDone = $script:ui.ShowDone.Checked; [void](Update-Panels) })
    $keepBtn.Add_Click({
        $d = $script:ActiveDoc
        if ($d) { $d.Stamp = Get-FileStamp -Path $d.Path; $d.SeenStamp = ''; $d.Conflict = ''; Update-Chrome }
    })
    $reloadBtn.Add_Click({ $d = $script:ActiveDoc; if ($d -and $d.Path) { try { Read-DocFromDisk -Doc $d } catch { Set-Status ("Could not reload: " + $_.Exception.Message) } } })
    $form.Add_FormClosing({ param($s, $e) if (-not (Confirm-CloseAll)) { $e.Cancel = $true } else { Save-AppState } })

    $timer = New-Object System.Windows.Forms.Timer
    $timer.Interval = 2000
    $timer.Add_Tick({
        try { Invoke-Poll } catch { Write-TrimpleLog ("poll: " + $_.Exception.Message) }
    })
    $script:ui.Timer = $timer

    $form.Add_Shown({
        try {
            $script:ui.Split.SplitterDistance = S ([int]$script:Settings.PanelWidth)
            $script:ui.LeftSplit.SplitterDistance = [int]($script:ui.LeftSplit.Height * 0.68)
        } catch { }
        [TrimpleNative]::SetCue($script:ui.Search.Handle, 'Search tasks')
        Apply-Theme
        $script:ui.Timer.Start()
        if ($script:ActiveDoc) { $script:ActiveDoc.Editor.Focus() | Out-Null }
    })
    $form.Add_Resize({ $script:ui.Strip.Invalidate() })
}

function Invoke-OpenDialog {
    $dlg = New-Object System.Windows.Forms.OpenFileDialog
    $dlg.Filter = 'Text files (*.txt)|*.txt|All files (*.*)|*.*'
    $dlg.Multiselect = $true
    if ($script:Folder -and (Test-Path -LiteralPath $script:Folder)) { $dlg.InitialDirectory = $script:Folder }
    $script:Busy = $true
    try { $r = $dlg.ShowDialog($script:ui.Form) } finally { $script:Busy = $false }
    $names = @($dlg.FileNames)
    $dlg.Dispose()
    if ($r -eq [System.Windows.Forms.DialogResult]::OK) { foreach ($n in $names) { [void](Open-Doc -Path $n) } }
}

function Confirm-CloseAll {
    foreach ($d in @($script:Docs)) { if (-not (Confirm-SaveDoc -Doc $d)) { return $false } }
    return $true
}

function Save-AppState {
    try {
        $form = $script:ui.Form
        $w = $script:Settings.Window
        $w.Maximized = ($form.WindowState -eq 'Maximized')
        if ($form.WindowState -eq 'Normal') { $w.X = $form.Left; $w.Y = $form.Top; $w.Width = $form.Width; $w.Height = $form.Height }
        if (-not $script:ui.Split.Panel1Collapsed) { $script:Settings.PanelWidth = [int]($script:ui.Split.SplitterDistance / $script:Scale) }
        $script:Settings.OpenFiles = @($script:Docs | Where-Object { $_.Path } | ForEach-Object { $_.Path })
        $script:Settings.Theme = $script:ThemeName
        [void](Export-TrimpleSettings -Settings $script:Settings -Paths (Get-TrimpleSettingsPaths -AppRoot $script:AppRoot))
    }
    catch { Write-TrimpleLog ("save state: " + $_.Exception.Message) }
}

# ------------------------------------------------------------------ entry points

function Initialize-TrimpleApp {
    param([string]$AppRoot, [string]$Folder = '', [string[]]$Open = @(), [bool]$Headless = $false)
    $script:AppRoot = $AppRoot
    $script:Headless = $Headless
    $script:Settings = Import-TrimpleSettings -Paths (Get-TrimpleSettingsPaths -AppRoot $AppRoot)
    $g = [System.Drawing.Graphics]::FromHwnd([IntPtr]::Zero)
    $script:Scale = $g.DpiX / 96.0
    $g.Dispose()
    Set-ThemeColors -Name ([string]$script:Settings.Theme)
    Initialize-Fonts
    New-TrimpleUI
    $script:ui.Form.CreateControl()
    $null = $script:ui.Form.Handle

    $w = $script:Settings.Window
    if (-not $Headless -and $w.X -ge 0 -and $w.Y -ge 0) {
        $bounds = New-Object System.Drawing.Rectangle([int]$w.X, [int]$w.Y, [int]$w.Width, [int]$w.Height)
        foreach ($scr in [System.Windows.Forms.Screen]::AllScreens) {
            if ($scr.WorkingArea.IntersectsWith($bounds)) { $script:ui.Form.StartPosition = 'Manual'; $script:ui.Form.Location = $bounds.Location; break }
        }
    }
    if (-not $Headless -and $w.Maximized) { $script:ui.Form.WindowState = 'Maximized' }

    $notes = Initialize-NotesFolder -Override $Folder
    Set-TaskPanelVisible -Visible ([bool]$script:Settings.TaskPanelVisible)
    Set-NotesFolder -Path $notes

    $toOpen = New-Object System.Collections.Generic.List[string]
    foreach ($o in @($Open)) { if ($o) { $toOpen.Add($o) } }
    if ($toOpen.Count -eq 0 -and -not $Folder) { foreach ($p in @($script:Settings.OpenFiles)) { if ($p -and (Test-Path -LiteralPath $p -PathType Leaf)) { $toOpen.Add([string]$p) } } }
    foreach ($p in $toOpen) { [void](Open-Doc -Path $p) }
    if ($script:Docs.Count -eq 0) { [void](New-UntitledDoc) }
    Apply-Theme
}

function Start-TrimpleApp {
    param([string]$AppRoot, [string]$Folder = '', [string[]]$Open = @())
    [System.Windows.Forms.Application]::SetUnhandledExceptionMode([System.Windows.Forms.UnhandledExceptionMode]::CatchException)
    [System.Windows.Forms.Application]::add_ThreadException({
        param($s, $e)
        Write-TrimpleLog ("ThreadException: " + $e.Exception.ToString())
        Set-Status ("Error: " + $e.Exception.Message)
    })
    Initialize-TrimpleApp -AppRoot $AppRoot -Folder $Folder -Open $Open
    [System.Windows.Forms.Application]::Run($script:ui.Form)
}
