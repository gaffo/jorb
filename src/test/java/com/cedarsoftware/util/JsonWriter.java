package com.cedarsoftware.util.io.util;

import java.io.BufferedWriter;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.OutputStream;
import java.io.OutputStreamWriter;
import java.io.UnsupportedEncodingException;
import java.io.Writer;
import java.lang.reflect.Array;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.lang.reflect.Modifier;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.text.DateFormat;
import java.text.SimpleDateFormat;
import java.util.Calendar;
import java.util.Collection;
import java.util.Date;
import java.util.HashMap;
import java.util.HashSet;
import java.util.IdentityHashMap;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.LinkedList;
import java.util.Map;
import java.util.Set;

/**
 * Output a Java object graph in JSON format.  This code handles cyclic
 * references and can serialize any Object graph without requiring a class
 * to be 'Serializeable' or have any specific methods on it.
 * <br/><ul><li>
 * Call the static method: <code>JsonWriter.toJson(employee)</code>.  This will
 * convert the passed in 'employee' instance into a JSON String.</li>
 * <li>Using streams:
 * <pre>     JsonWriter writer = new JsonWriter(stream);
 *     writer.write(employee);
 *     writer.close();</pre>
 * This will write the 'employee' object to the passed in OutputStream.
 * </li>
 * <p>That's it.  This can be used as a debugging tool.  Output an object
 * graph using the above code.  You can copy that JSON output into this site
 * which formats it with a lot of whitespace to make it human readable:
 * http://jsonformatter.curiousconcept.com/
 * <br/><br/>
 * <p>This will output any object graph deeply (or null).  Object references are
 * properly handled.  For example, if you had A->B, B->C, and C->A, then
 * A will be serialized with a B object in it, B will be serialized with a C
 * object in it, and then C will be serialized with a reference to A (ref), not a
 * redefinition of A.</p>
 * <br/>
 * If a <code>void _writeJson(Writer writer)</code> method is found on an object, then that
 * method will be called to output the JSON for that object.  A corresponding
 * <code>void _readJson(Map map)</code> method is looked for and called if it exists.  These
 * should rarely (if ever) be used.  See the TestJsonReaderWriter for an example of this
 * usage.<br/><br/>
 *
 * @author John DeRegnaucourt (jdereg@gmail.com)
 *         <br/>
 *         Copyright (c) John DeRegnaucourt
 *         <br/><br/>
 *         Licensed under the Apache License, Version 2.0 (the "License");
 *         you may not use this file except in compliance with the License.
 *         You may obtain a copy of the License at
 *         <br/><br/>
 *         http://www.apache.org/licenses/LICENSE-2.0
 *         <br/><br/>
 *         Unless required by applicable law or agreed to in writing, software
 *         distributed under the License is distributed on an "AS IS" BASIS,
 *         WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *         See the License for the specific language governing permissions and
 *         limitations under the License.
 */
public class JsonWriter extends Writer
{
    private final Map<Object, Long> _objVisited = new IdentityHashMap<Object, Long>();
    private final Map<Object, Long> _objsReferenced = new IdentityHashMap<Object, Long>();
    private static final Map<String, ClassMeta> _classMetaCache = new HashMap<String, ClassMeta>();
    private static Object[] _byteStrings = new Object[256];
    private static final Set<Class> _prims = new HashSet<Class>();
    private Writer _out;
    private long _identity = 1;
    private boolean _prettyMode = true;
    private DateFormat _dateFormat = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSSZ (z)");

    static
    {
        for (short i = -128; i <= 127; i++)
        {
            char[] chars = Integer.toString(i).toCharArray();
            _byteStrings[i + 128] = chars;
        }

        _prims.add(Byte.class);
        _prims.add(Integer.class);
        _prims.add(Long.class);
        _prims.add(Double.class);
        _prims.add(Character.class);
        _prims.add(Float.class);
        _prims.add(Boolean.class);
        _prims.add(Short.class);
    }

    static class ClassMeta extends LinkedHashMap<String, Field>
    {
        Method _writeMethod = null;
        Method _readMethod = null;
    }

    /**
     * Convert a Java Object to a JSON String.  This is the
     * easy-to-use API - it returns null if there was an error.
     *
     * @param item Object to convert to a JSON String.
     * @return String containing JSON representation of passed
     *         in object, or null if an error occurred.
     */
    public static String toJson(Object item)
    {
        return toJson(item, true);
    }

    /**
     * Convert a Java Object to a JSON String.
     *
     * @param item Object to convert to a JSON String.
     * @return String containing JSON representation of passed
     *         in object.
     * @throws java.io.IOException If an I/O error occurs
     */
    public static String objectToJson(Object item) throws IOException
    {
        return objectToJson(item, true);
    }

    /**
     * Convert a Java Object to a JSON String.  This is the
     * easy-to-use API - it returns null if there was an error.
     *
     * @param item Object to convert to a JSON String.
     * @return String containing JSON representation of passed
     *         in object, or null if an error occurred.
     */
    public static String toJson(Object item, boolean prettyMode)
    {
        try
        {
            return objectToJson(item);
        }
        catch (IOException ignored)
        {
            return null;
        }
    }

    /**
     * Convert a Java Object to a JSON String.
     *
     * @param item Object to convert to a JSON String.
     * @return String containing JSON representation of passed
     *         in object.
     * @throws java.io.IOException If an I/O error occurs
     */
    public static String objectToJson(Object item, boolean prettyMode) throws IOException
    {
        ByteArrayOutputStream stream = new ByteArrayOutputStream();
        JsonWriter writer = new JsonWriter(stream, prettyMode);
        writer.write(item);
        writer.close();
        return new String(stream.toByteArray(), "UTF-8");
    }

    public JsonWriter(OutputStream out) throws IOException
    {
        this(out, true);
    }

    public JsonWriter(OutputStream out, boolean prettyMode) throws IOException
    {
        try
        {
            _prettyMode = prettyMode;
            _out = new BufferedWriter(new OutputStreamWriter(out, "UTF-8"));
        }
        catch (UnsupportedEncodingException e)
        {
            throw new IOException("Unsupported encoding.  Get a JVM that supports UTF-8", e);
        }
    }

    public void write(Object obj) throws IOException
    {
        traceReferences(obj);
        _objVisited.clear();
        writeImpl(obj, true);
        flush();
        _objVisited.clear();
        _objsReferenced.clear();
    }

    private void traceReferences(Object root)
    {
        LinkedList<Object> stack = new LinkedList<Object>();
        stack.addFirst(root);

        while (!stack.isEmpty())
        {
            Object obj = stack.removeFirst();
            if (obj == null)
            {
                continue;
            }

            Long id = _objVisited.get(obj);
            if (id != null)
            {   // Only write an object once.
                _objsReferenced.put(obj, id);
                continue;
            }

            _objVisited.put(obj, _identity++);
            final Class clazz = obj.getClass();

            if (isPrimitiveWrapper(clazz) || clazz.equals(String.class) || clazz.equals(Class.class))
            {   // Can't reference anything
                continue;
            }

            if (clazz.isArray())
            {
                Class compType = clazz.getComponentType();
                if (!compType.isPrimitive())
                {   // Speed up: do not trace references of primitive array types - they cannot reference objects, other than immutables.
                    int len = Array.getLength(obj);

                    for (int i = 0; i < len; i++)
                    {
                        stack.add(Array.get(obj, i));
                    }
                }
            }
            else
            {
                traceFields(stack, obj);
            }
        }
    }

    private void traceFields(LinkedList<Object> stack, Object obj)
    {
        ClassMeta fields = getDeepDeclaredFields(obj.getClass());

        for (Field field : fields.values())
        {
            try
            {
                if (isPrimitiveWrapper(field.getType()))
                {    // speed up: primitives & primitive wrappers cannot reference another object
                    continue;
                }

                Object o = field.get(obj);
                if (o != null)
                {
                    stack.addFirst(o);
                }
            }
            catch (IllegalAccessException ignored) { }
        }
    }

    private boolean writeOptionalReference(Object obj) throws IOException
    {
        if (_objVisited.containsKey(obj))
        {    // Only write (define) an object once in the JSON stream, otherwise emit a @ref
            _out.write("{\"@ref\":");
            String id = getId(obj);
            if (id == null)
            {   // TODO: remove this if once it is determined this can never happen
                _out.write("0}");
                return true;
            }
            _out.write(id);
            _out.write('}');
            return true;
        }

        // Mark the object as visited by putting it in the Map (this map is re-used / clear()'d after walk()).
        _objVisited.put(obj, null);
        return false;
    }

    private void writeImpl(Object obj, boolean showType) throws IOException
    {
        if (obj == null)
        {
            _out.write("{}");
            return;
        }

        if (obj.getClass().isArray())
        {
            writeArray(obj, showType);
        }
        else
        {
            if (_prettyMode)
            {   // Optional [default] special handling for Collection's and Maps to make output smaller, easier to read.
                if (obj instanceof Collection)
                {
                    writeCollection((Collection) obj, showType);
                }
                else if (obj instanceof Map)
                {
                    writeMap((Map) obj, showType);
                }
                else
                {
                    writeObject(obj, showType);
                }
            }
            else
            {
                writeObject(obj, showType);
            }
        }
    }

    private void writeId(String id) throws IOException
    {
        _out.write("\"@id\":");
        _out.write(id);
    }

    private void writeType(Object obj) throws IOException
    {
        _out.write("\"@type\":\"");
        Class c = obj.getClass();

        if (String.class.equals(c))
        {
            _out.write("string");
        }
        else if (Boolean.class.equals(c))
        {
            _out.write("boolean");
        }
        else if (Byte.class.equals(c))
        {
            _out.write("byte");
        }
        else if (Short.class.equals(c))
        {
            _out.write("short");
        }
        else if (Integer.class.equals(c))
        {
            _out.write("int");
        }
        else if (Long.class.equals(c))
        {
            _out.write("long");
        }
        else if (Double.class.equals(c))
        {
            _out.write("double");
        }
        else if (Float.class.equals(c))
        {
            _out.write("float");
        }
        else if (Character.class.equals(c))
        {
            _out.write("char");
        }
        else if (Date.class.equals(c))
        {
            _out.write("date");
        }
        else if (Class.class.equals(c))
        {
            _out.write("class");
        }
        else
        {
            _out.write(c.getName());
        }
        _out.write('"');
    }

    private void writeIdAndType(Object col, boolean showType, boolean referenced) throws IOException
    {
        if (showType || referenced)
        {
            if (referenced)
            {
                String id = getId(col);
                if (id == null)
                {   // TODO: Remove this check if we determine this can never happen
                    writeId("0");
                }
                else
                {
                    writeId(id);
                }
            }

            if (showType)
            {
                if (referenced)
                {
                    _out.write(",");
                }
                writeType(col);
            }
        }
    }

    /**
     * Specialized output for 'Class' to make it easy to read and create.
     */
    private void writeClass(Object obj, boolean showType) throws IOException
    {
        String value = ((Class) obj).getName();
        if (showType)
        {
            _out.write('{');
            writeType(obj);
            _out.write(",\"value\":");
            writeJsonUtf8String(value);
            _out.write('}');
        }
        else
        {
            writeJsonUtf8String(value);
        }
    }

    /**
     * Specialized output for certain classes to make them easy to read and create.
     */
    private void writeSpecial(Object obj, boolean showType, String value) throws IOException
    {
        boolean referenced = _objsReferenced.containsKey(obj);
        boolean hasHeader = showType | referenced;

        if (hasHeader)
        {
            _out.write('{');
        }

        writeIdAndType(obj, showType, referenced);

        if (hasHeader)
        {
            _out.write(",\"value\":\"");
            _out.write(value);
            _out.write("\"}");
        }
        else
        {
            _out.write('"');
            _out.write(value);
            _out.write('"');
        }
    }

    private void writeDate(Object obj, boolean showType) throws IOException
    {
        String value = Long.toString(((Date) obj).getTime());

        if (showType)
        {
            _out.write('{');
            writeType(obj);
            _out.write(",\"value\":");
            _out.write(value);
            _out.write('}');
        }
        else
        {
            _out.write(value);
        }
    }

    private void writePrimitive(Object obj) throws IOException
    {
        String value = obj.toString();

        if (obj instanceof Character)
        {
            _out.write('"');
            _out.write((Character) obj);
            _out.write('"');
        }
        else
        {
            _out.write(value);
        }
    }

    private void writeArray(Object array, boolean showType) throws IOException
    {
        if (writeOptionalReference(array))
        {
            return;
        }

        Class arrayType = array.getClass();
        int len = Array.getLength(array);
        boolean referenced = _objsReferenced.containsKey(array);
        boolean typeWritten = showType && !Object[].class.equals(arrayType);

        boolean hasHeader = typeWritten | referenced;
        if (hasHeader)
        {
            _out.write('{');
        }

        writeIdAndType(array, typeWritten, referenced);

        if (hasHeader)
        {
            _out.write(',');
        }

        if (len == 0)
        {
            if (hasHeader)
            {
                _out.write("\"@items\":[]}");
            }
            else
            {
                _out.write("[]");
            }
            return;
        }

        if (hasHeader)
        {
            _out.write("\"@items\":[");
        }
        else
        {
            _out.write('[');
        }

        final int lenMinus1 = len - 1;

        // Intentionally processing each primitive array type in separate
        // custom loop for speed. All of them could be handled using
        // reflective Array.get() but it is slower.  I chose speed over code length.
        if (byte[].class.equals(arrayType))
        {
            writeByteArray((byte[]) array, lenMinus1);
        }
        else if (char[].class.equals(arrayType))
        {
            writeJsonUtf8String(new String((char[]) array));
        }
        else if (short[].class.equals(arrayType))
        {
            writeShortArray((short[]) array, lenMinus1);
        }
        else if (int[].class.equals(arrayType))
        {
            writeIntArray((int[]) array, lenMinus1);
        }
        else if (long[].class.equals(arrayType))
        {
            writeLongArray((long[]) array, lenMinus1);
        }
        else if (float[].class.equals(arrayType))
        {
            writeFloatArray((float[]) array, lenMinus1);
        }
        else if (double[].class.equals(arrayType))
        {
            writeDoubleArray((double[]) array, lenMinus1);
        }
        else if (boolean[].class.equals(arrayType))
        {
            writeBooleanArray((boolean[]) array, lenMinus1);
        }
        else
        {
            final Class componentClass = array.getClass().getComponentType();
            final boolean isStringArray = String[].class.equals(arrayType);
            final boolean isDateArray = Date[].class.equals(arrayType);
            final boolean isPrimitiveArray = isPrimitiveWrapper(componentClass);
            final boolean isClassArray = Class[].class.equals(arrayType);
            final boolean isObjectArray = Object[].class.equals(arrayType);

            for (int i = 0; i < len; i++)
            {
                final Object value = Array.get(array, i);

                if (value == null)
                {
                    _out.write("null");
                }
                else if (isStringArray)
                {
                    writeJsonUtf8String((String) value);
                }
                else if (isDateArray)
                {
                    _out.write(Long.toString(((Date) value).getTime()));
                }
                else if (isClassArray)
                {
                    writeClass(value, false);
                }
                else if (isPrimitiveArray)
                {
                    writePrimitive(value);
                }
                else if (isObjectArray)
                {
                    if (value instanceof Boolean || value instanceof Long || value instanceof Double)
                    {   // These types can be inferred - true | false, long integer number (- or digits), or double precision floating point (., e)
                        writePrimitive(value);
                    }
                    else
                    {
                        writeImpl(value, true);
                    }
                }
                else
                {   // Specific Class-type arrays - only force type when
                    // the instance is derived from array base class.
                    boolean forceType = value.getClass() != componentClass;
                    writeImpl(value, forceType);
                }

                if (i != lenMinus1)
                {
                    _out.write(',');
                }
            }
        }

        _out.write(']');
        if (typeWritten || referenced)
        {
            _out.write('}');
        }
    }

    private void writeBooleanArray(boolean[] booleans, int lenMinus1) throws IOException
    {
        for (int i = 0; i < lenMinus1; i++)
        {
            _out.write(booleans[i] ? "true," : "false,");
        }
        _out.write(Boolean.toString(booleans[lenMinus1]));
    }

    private void writeDoubleArray(double[] doubles, int lenMinus1) throws IOException
    {
        for (int i = 0; i < lenMinus1; i++)
        {
            _out.write(Double.toString(doubles[i]));
            _out.write(',');
        }
        _out.write(Double.toString(doubles[lenMinus1]));
    }

    private void writeFloatArray(float[] floats, int lenMinus1) throws IOException
    {
        for (int i = 0; i < lenMinus1; i++)
        {
            _out.write(Double.toString(floats[i]));
            _out.write(',');
        }
        _out.write(Float.toString(floats[lenMinus1]));
    }

    private void writeLongArray(long[] longs, int lenMinus1) throws IOException
    {
        for (int i = 0; i < lenMinus1; i++)
        {
            _out.write(Long.toString(longs[i]));
            _out.write(',');
        }
        _out.write(Long.toString(longs[lenMinus1]));
    }

    private void writeIntArray(int[] ints, int lenMinus1) throws IOException
    {
        for (int i = 0; i < lenMinus1; i++)
        {
            _out.write(Integer.toString(ints[i]));
            _out.write(',');
        }
        _out.write(Integer.toString(ints[lenMinus1]));
    }

    private void writeShortArray(short[] shorts, int lenMinus1) throws IOException
    {
        for (int i = 0; i < lenMinus1; i++)
        {
            _out.write(Integer.toString(shorts[i]));
            _out.write(',');
        }
        _out.write(Integer.toString(shorts[lenMinus1]));
    }

    private void writeByteArray(byte[] bytes, int lenMinus1) throws IOException
    {
        for (int i = 0; i < lenMinus1; i++)
        {
            _out.write((char[]) _byteStrings[bytes[i] + 128]);
            _out.write(',');
        }
        _out.write((char[]) _byteStrings[bytes[lenMinus1] + 128]);
    }

    private void writeCollection(Collection col, boolean showType) throws IOException
    {
        if (writeOptionalReference(col))
        {
            return;
        }

        boolean referenced = _objsReferenced.containsKey(col);

        _out.write("{");
        writeIdAndType(col, showType, referenced);

        int len = col.size();

        if (len == 0)
        {
            _out.write("}");
            return;
        }

        if (showType || referenced)
        {
            _out.write(",");
        }

        _out.write("\"@items\":[");
        Iterator i = col.iterator();

        while (i.hasNext())
        {
            writeCollectionElement(i.next());

            if (i.hasNext())
            {
                _out.write(",");
            }

        }
        _out.write("]}");
    }

    private void writeMap(Map map, boolean showType) throws IOException
    {
        if (writeOptionalReference(map))
        {
            return;
        }

        boolean referenced = _objsReferenced.containsKey(map);

        _out.write("{");
        writeIdAndType(map, showType, referenced);

        if (map.isEmpty())
        {
            _out.write("}");
            return;
        }

        if (showType || referenced)
        {
            _out.write(",");
        }

        _out.write("\"@keys\":[");
        Iterator i = map.keySet().iterator();

        while (i.hasNext())
        {
            writeCollectionElement(i.next());

            if (i.hasNext())
            {
                _out.write(",");
            }
        }

        _out.write("],\"@items\":[");
        i = map.values().iterator();

        while (i.hasNext())
        {
            writeCollectionElement(i.next());

            if (i.hasNext())
            {
                _out.write(",");
            }
        }

        _out.write("]}");
    }

    /**
     * Write an element that is contained in some type of collection or Map.
     * @param o Collection element to output in JSON format.
     */
    private void writeCollectionElement(Object o) throws IOException
    {
        if (o == null)
        {
            _out.write("null");
        }
        else if (o instanceof Boolean || o instanceof Long || o instanceof Double)
        {
            _out.write(o.toString());
        }
        else if (o instanceof String)
        {
            writeJsonUtf8String(o.toString());
        }
        else
        {
            writeImpl(o, true);
        }
    }

    /**
     * String, Date, and Class have been written before calling this method,
     * strictly for performance.
     *
     * @param obj      Object to be written in JSON format
     * @param showType boolean true means show the "@type" field, false
     *                 eliminates it.  Many times the type can be dropped because it can be
     *                 inferred from the field or array type.
     * @throws IOException if an error occurs writing to the output stream.
     */
    private void writeObject(Object obj, boolean showType) throws IOException
    {
        if (obj instanceof String)
        {
            writeJsonUtf8String((String) obj);
            return;
        }
        else if (obj.getClass().equals(Date.class))
        {
            writeDate(obj, showType);
            return;
        }
        else if (obj instanceof Class)
        {
            writeClass(obj, showType);
            return;
        }

        if (writeOptionalReference(obj))
        {
            return;
        }

        if (obj instanceof  Calendar)
        {
            Calendar cal = (Calendar) obj;
            _dateFormat.setTimeZone(cal.getTimeZone());
            String date = _dateFormat.format(cal.getTime());
            writeSpecial(obj, showType, date);
            return;
        }
        else if (obj instanceof BigDecimal)
        {
            writeSpecial(obj, showType, ((BigDecimal) obj).toPlainString());
            return;
        }
        else if (obj instanceof BigInteger)
        {
            writeSpecial(obj, showType, obj.toString());
            return;
        }
        else if (obj instanceof java.sql.Date)
        {
            writeSpecial(obj, showType, obj.toString());
            return;
        }

        _out.write('{');
        boolean referenced = _objsReferenced.containsKey(obj);
        if (referenced)
        {
            writeId(getId(obj));
        }

        ClassMeta classInfo = getDeepDeclaredFields(obj.getClass());
        if (classInfo._writeMethod != null)
        {   // Must show type when class has custom _writeJson() method on it.
            // The JsonReader uses this to know it is dealing with an object
            // that has custom json io methods on it.
            showType = true;
        }

        if (referenced && showType)
        {
            _out.write(',');
        }

        if (showType)
        {
            writeType(obj);
        }

        boolean first = !showType;
        if (referenced && !showType)
        {
            first = false;
        }

        if (classInfo._writeMethod == null)
        {
            for (Field field : classInfo.values())
            {
                if (_prettyMode && (field.getModifiers() & Modifier.TRANSIENT) != 0)
                {   // Skip transient fields when in 'prettyMode'
                    continue;
                }

                if (first)
                {
                    first = false;
                }
                else
                {
                    _out.write(',');
                }

                writeJsonUtf8String(field.getName());
                _out.write(':');

                Object o;
                try
                {
                    o = field.get(obj);
                }
                catch (IllegalAccessException e)
                {
                    o = null;
                }

                if (o == null)
                {    // don't quote null
                    _out.write("null");
                    continue;
                }

                Class type = field.getType();
                boolean forceType = o.getClass() != type;     // If types are not exactly the same, write "@type" field

                if (isPrimitiveWrapper(type))
                {
                    writePrimitive(o);
                }
                else
                {
                    writeImpl(o, forceType);
                }
            }
        }
        else
        {   // Invoke custom _writeJson() method.
            _out.write(',');
            try
            {
                classInfo._writeMethod.invoke(obj, _out);
            }
            catch (Exception e)
            {
                throw new IOException("Error invoking " + obj.getClass() + "._jsonWrite()", e);
            }
        }

        _out.write('}');
    }

    private static boolean isPrimitiveWrapper(Class c)
    {
        return c.isPrimitive() || _prims.contains(c);
    }

    /**
     * Write out special characters "\b, \f, \t, \n, \r", as such, backslash as \\
     * quote as \" and values less than an ASCII space (20hex) as "\\u00xx" format,
     * characters in the range of ASCII space to a '~' as ASCII, and anything higher in UTF-8.
     *
     * @param s String to be written in utf8 format on the output stream.
     * @throws IOException if an error occurs writing to the output stream.
     */
    private void writeJsonUtf8String(String s) throws IOException
    {
        _out.write('\"');
        int len = s.length();

        for (int i = 0; i < len; i++)
        {
            char c = s.charAt(i);

            if (c < ' ')
            {    // Anything less than ASCII space, write either in \\u00xx form, or the special \t, \n, etc. form
                if (c == '\b')
                {
                    _out.write("\\b");
                }
                else if (c == '\t')
                {
                    _out.write("\\t");
                }
                else if (c == '\n')
                {
                    _out.write("\\n");
                }
                else if (c == '\f')
                {
                    _out.write("\\f");
                }
                else if (c == '\r')
                {
                    _out.write("\\r");
                }
                else
                {
                    String hex = Integer.toHexString(c);
                    _out.write("\\u");
                    int pad = 4 - hex.length();
                    for (int k = 0; k < pad; k++)
                    {
                        _out.write('0');
                    }
                    _out.write(hex);
                }
            }
            else if (c == '\\' || c == '"')
            {
                _out.write('\\');
                _out.write(c);
            }
            else
            {   // Anything else - write in UTF-8 form (multi-byte encoded) (OutputStreamWriter is UTF-8)
                _out.write(c);
            }
        }
        _out.write('\"');
    }

    /**
     * @param c Class instance
     * @return ClassMeta which contains fields of class and customer write/read
     *         methods if they exist. The results are cached internally for performance
     *         when called again with same Class.
     */
    static ClassMeta getDeepDeclaredFields(Class c)
    {
        ClassMeta classInfo = _classMetaCache.get(c.getName());
        if (classInfo != null)
        {
            return classInfo;
        }

        classInfo = new ClassMeta();
        Class curr = c;

        while (curr != null)
        {
            try
            {
                Field[] local = curr.getDeclaredFields();

                for (Field field : local)
                {
                    if (!field.isAccessible())
                    {
                        try
                        {
                            field.setAccessible(true);
                        }
                        catch (Exception ignored)
                        { }
                    }

                    if ((field.getModifiers() & Modifier.STATIC) == 0)
                    {    // speed up: do not process static fields.
                        classInfo.put(field.getName(), field);
                    }
                }
            }
            catch (ThreadDeath t)
            {
                throw t;
            }
            catch (Throwable ignored)
            { }

            curr = curr.getSuperclass();
        }

        try
        {
            classInfo._writeMethod = c.getDeclaredMethod("_writeJson", new Class[]{Writer.class});
        }
        catch (Exception ignored)
        { }

        try
        {
            classInfo._readMethod = c.getDeclaredMethod("_readJson", new Class[]{Map.class});
        }
        catch (Exception ignored)
        { }

        _classMetaCache.put(c.getName(), classInfo);
        return classInfo;
    }

    public void flush()
    {
        try
        {
            if (_out != null)
            {
                _out.flush();
            }
        }
        catch (IOException ignored)
        {
        }
    }

    public void close()
    {
    }

    public void write(char[] cbuf, int off, int len) throws IOException
    {
        _out.write(cbuf, off, len);
    }

    private String getId(Object o)
    {
        Long id = _objsReferenced.get(o);
        return id == null ? null : Long.toString(id);
    }
}
