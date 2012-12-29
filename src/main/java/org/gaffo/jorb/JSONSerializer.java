package org.gaffo.jorb;

import com.cedarsoftware.util.io.JsonReader;
import com.cedarsoftware.util.io.JsonWriter;
import org.jboss.netty.buffer.ChannelBufferInputStream;

import java.io.IOException;
import java.io.OutputStream;

public class JSONSerializer
{

    public String toJson(Object o)
    {
        try
        {
            return JsonWriter.objectToJson(o);
        }
        catch (IOException e)
        {
            // never gonna happen
            e.printStackTrace();
            return "";
        }
    }

    public void toJson(Object o, OutputStream os) throws IOException
    {
        new JsonWriter(os).write(o);
    }

    public <T> T fromJson(String data)
    {
        return (T) JsonReader.toJava(data);
    }

    public <T> T fromJson(ChannelBufferInputStream is) throws IOException
    {
        return (T) new JsonReader(is).readObject();
    }
}
