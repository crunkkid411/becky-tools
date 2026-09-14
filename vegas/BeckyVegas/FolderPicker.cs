using System;
using System.IO;
using System.Runtime.InteropServices;

namespace BeckyVegas
{
    // FolderPicker shows the modern Windows "Select Folder" dialog (the Explorer-style
    // one with the address bar and search box), not the old tree-view
    // FolderBrowserDialog, which is tiny and hard to read. Plain COM interop against
    // IFileOpenDialog; returns null when cancelled.
    internal static class FolderPicker
    {
        const uint FOS_PICKFOLDERS = 0x20;
        const uint FOS_FORCEFILESYSTEM = 0x40;
        const uint SIGDN_FILESYSPATH = 0x80058000;
        const int ERROR_CANCELLED_HR = unchecked((int)0x800704C7);

        internal static string Pick(IntPtr owner, string title, string startFolder)
        {
            IFileDialog dialog = (IFileDialog)new FileOpenDialogCom();
            try
            {
                uint options;
                dialog.GetOptions(out options);
                dialog.SetOptions(options | FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM);
                dialog.SetTitle(title);
                if (!string.IsNullOrEmpty(startFolder) && Directory.Exists(startFolder))
                {
                    IShellItem start;
                    if (SHCreateItemFromParsingName(startFolder, IntPtr.Zero, typeof(IShellItem).GUID, out start) == 0)
                    {
                        dialog.SetFolder(start);
                        Marshal.ReleaseComObject(start);
                    }
                }
                int hr = dialog.Show(owner);
                if (hr == ERROR_CANCELLED_HR || hr != 0)
                {
                    return null;
                }
                IShellItem result;
                dialog.GetResult(out result);
                string path;
                result.GetDisplayName(SIGDN_FILESYSPATH, out path);
                Marshal.ReleaseComObject(result);
                return path;
            }
            finally
            {
                Marshal.ReleaseComObject(dialog);
            }
        }

        [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
        static extern int SHCreateItemFromParsingName(string path, IntPtr bindCtx, [MarshalAs(UnmanagedType.LPStruct)] Guid riid, out IShellItem item);

        [ComImport, Guid("DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7")]
        class FileOpenDialogCom
        {
        }

        [ComImport, Guid("42f85136-db7e-439c-85f1-e4075d135fc8"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
        interface IFileDialog
        {
            [PreserveSig] int Show(IntPtr parent);
            void SetFileTypes(uint count, IntPtr filterSpec);
            void SetFileTypeIndex(uint index);
            void GetFileTypeIndex(out uint index);
            void Advise(IntPtr events, out uint cookie);
            void Unadvise(uint cookie);
            void SetOptions(uint options);
            void GetOptions(out uint options);
            void SetDefaultFolder(IShellItem item);
            void SetFolder(IShellItem item);
            void GetFolder(out IShellItem item);
            void GetCurrentSelection(out IShellItem item);
            void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string name);
            void GetFileName([MarshalAs(UnmanagedType.LPWStr)] out string name);
            void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string title);
            void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string text);
            void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string label);
            void GetResult(out IShellItem item);
            void AddPlace(IShellItem item, int placement);
            void SetDefaultExtension([MarshalAs(UnmanagedType.LPWStr)] string extension);
            void Close(int hr);
            void SetClientGuid(ref Guid guid);
            void ClearClientData();
            void SetFilter(IntPtr filter);
        }

        [ComImport, Guid("43826D1E-E718-42EE-BC55-A1E261C37BFE"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
        interface IShellItem
        {
            void BindToHandler(IntPtr bindCtx, ref Guid handler, ref Guid riid, out IntPtr result);
            void GetParent(out IShellItem parent);
            void GetDisplayName(uint sigdn, [MarshalAs(UnmanagedType.LPWStr)] out string name);
            void GetAttributes(uint mask, out uint attributes);
            void Compare(IShellItem other, uint hint, out int order);
        }
    }
}
