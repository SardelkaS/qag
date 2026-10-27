using System;
using System.Diagnostics;
using System.Runtime.InteropServices;
using System.Threading;
using System.Collections.Generic;

[ComImport, Guid("00000016-0000-0000-C000-000000000046"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
public interface IQagOleMessageFilter {
    [PreserveSig] int HandleInComingCall(int type, IntPtr caller, int ticks, IntPtr info);
    [PreserveSig] int RetryRejectedCall(IntPtr callee, int ticks, int rejectType);
    [PreserveSig] int MessagePending(IntPtr callee, int ticks, int pendingType);
}
public sealed class QagOfficeFilter : IQagOleMessageFilter {
    public int HandleInComingCall(int type, IntPtr caller, int ticks, IntPtr info) { return 0; }
    public int RetryRejectedCall(IntPtr callee, int ticks, int rejectType) { return ticks < 30000 && (rejectType == 1 || rejectType == 2) ? 100 : -1; }
    public int MessagePending(IntPtr callee, int ticks, int pendingType) { return 2; }
    [DllImport("ole32.dll")] public static extern int CoRegisterMessageFilter(IQagOleMessageFilter filter, out IQagOleMessageFilter old);
}
public static class QagWordPasteCheck {
    delegate bool WindowCallback(IntPtr hwnd,IntPtr data);
    [DllImport("user32.dll")] static extern bool EnumWindows(WindowCallback callback,IntPtr data);
    [DllImport("user32.dll")] static extern bool EnumChildWindows(IntPtr hwnd,WindowCallback callback,IntPtr data);
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetWindowText(IntPtr hwnd,System.Text.StringBuilder text,int size);
    [DllImport("user32.dll",CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr hwnd,System.Text.StringBuilder text,int size);
    [DllImport("user32.dll")] static extern int GetDlgCtrlID(IntPtr hwnd);
    [DllImport("user32.dll")] static extern IntPtr SendMessageTimeout(IntPtr hwnd,uint message,IntPtr wparam,IntPtr lparam,uint flags,uint timeout,out IntPtr result);
    static string WindowText(IntPtr hwnd){var text=new System.Text.StringBuilder(2048);GetWindowText(hwnd,text,2048);return text.ToString();}
    static string WindowClass(IntPtr hwnd){var text=new System.Text.StringBuilder(256);GetClassName(hwnd,text,256);return text.ToString();}
    // Dismiss only the default-program question in our new automation instance.
    // No file associations or persistent preferences are changed.
    static void DismissDefaultProgramQuestion(int target){
        for(int retry=0;retry<30;retry++){
            bool dismissed=false;
            EnumWindows((hwnd,data)=>{
                uint pid;GetWindowThreadProcessId(hwnd,out pid);if(pid!=(uint)target||WindowClass(hwnd)!="#32770")return true;
                string message="";IntPtr no=IntPtr.Zero;
                EnumChildWindows(hwnd,(child,unused)=>{string type=WindowClass(child),text=WindowText(child);if(type=="Static")message+=" "+text;if(type=="Button"&&(text.Replace("&","").Trim().Equals("Нет",StringComparison.OrdinalIgnoreCase)||text.Replace("&","").Trim().Equals("No",StringComparison.OrdinalIgnoreCase)))no=child;return true;},IntPtr.Zero);
                if(no!=IntPtr.Zero&&(message.Contains("программой по умолчанию")||message.Contains("default program"))){IntPtr result;SendMessageTimeout(hwnd,0x0111,new IntPtr(GetDlgCtrlID(no)),no,2,2000,out result);dismissed=true;return false;}
                return true;
            },IntPtr.Zero);
            if(dismissed)return;Thread.Sleep(100);
        }
    }
    public static int Verify(string text, string docPath, int minimumImages, int appPid, string appPath) {
        IQagOleMessageFilter old;
        QagOfficeFilter.CoRegisterMessageFilter(new QagOfficeFilter(), out old);
        dynamic app = null, doc = null, offline = null;
        bool owned = false;
        int wordPid = 0;
        string stage="activation";
        HashSet<int> existing=new HashSet<int>();foreach(Process process in Process.GetProcessesByName("WINWORD")){existing.Add(process.Id);process.Dispose();}
        try {
            app = Activator.CreateInstance(Type.GetTypeFromProgID("Word.Application", true));
            if ((int)app.Documents.Count != 0) throw new InvalidOperationException("New Word instance already contains documents");
            foreach(Process process in Process.GetProcessesByName("WINWORD")){if(!existing.Contains(process.Id)){if(wordPid!=0)throw new InvalidOperationException("Ambiguous Word process ownership");wordPid=process.Id;}process.Dispose();}
            if(wordPid==0)throw new InvalidOperationException("Word did not create a separate automation process");
            owned = true;
            app.Visible = false; app.DisplayAlerts = 0;
            stage="create document";
            doc = app.Documents.Add();
            DismissDefaultProgramQuestion(wordPid);
            stage="document range";
            dynamic range = doc.Content;
            if (range == null) throw new InvalidOperationException("Word did not provide a document range");
            stage="paste";range.Paste();
            int count = (int)doc.InlineShapes.Count;
            if (count < minimumImages) throw new InvalidOperationException("Word lost embedded PNG parts");
            if (!((string)doc.Content.Text).Contains(text)) throw new InvalidOperationException("Word lost editable text");
            stage="save DOCX";doc.SaveAs2(docPath, 12);
            using (Process process = Process.GetProcessById(appPid)) {
                if (!String.Equals(process.MainModule.FileName, appPath, StringComparison.OrdinalIgnoreCase)) throw new InvalidOperationException("Test app path changed");
                process.Kill(); process.WaitForExit(10000);
            }
            offline = app.Documents.Add();
            stage="offline paste";
            offline.Content.Paste();
            if ((int)offline.InlineShapes.Count != count || !((string)offline.Content.Text).Contains(text)) throw new InvalidOperationException("Offline paste lost embedded content");
            return count;
        } catch(Exception ex){throw new InvalidOperationException("Word "+stage+": "+ex.Message,ex);} finally {
            if (owned) {
                if ((object)offline != null) { try { offline.Close(0); } catch {} }
                if ((object)doc != null) { try { doc.Close(0); } catch {} }
                if ((object)app != null) { try { app.Quit(0); } catch {} }
            }
            Release((object)offline);Release((object)doc);Release((object)app);
            QagOfficeFilter.CoRegisterMessageFilter(old, out old);
            if (owned && wordPid != 0) { try { using (Process process=Process.GetProcessById(wordPid)) { if (!process.WaitForExit(3000)) process.Kill(); } } catch (ArgumentException) {} }
        }
    }
    static void Release(object value){if(value!=null){try{Marshal.FinalReleaseComObject(value);}catch{}}}
}
