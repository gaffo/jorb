package com.cedarsoftware.util.io.util;

import java.io.BufferedReader;
import java.io.ByteArrayInputStream;
import java.io.FilterReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.Reader;
import java.io.UnsupportedEncodingException;
import java.lang.reflect.Array;
import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.InvocationTargetException;
import java.math.BigDecimal;
import java.math.BigInteger;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Calendar;
import java.util.Collection;
import java.util.Date;
import java.util.HashMap;
import java.util.HashSet;
import java.util.IdentityHashMap;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.LinkedList;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TimeZone;

/**
 * Read an object graph in JSON format and make it available in Java objects, or
 * in a "Map of Maps." (untyped representation).  This code handles cyclic references
 * and can deserialize any Object graph without requiring a class to be 'Serializeable'
 * or have any specific methods on it.  It will handle classes with non public constructors.
 * <br/><br/>
 * Usages:
 * <ul><li>
 * Call the static method: <code>JsonReader.toJava(String json)</code>.  This will
 * return a typed Java object graph.</li>
 * <li>
 * Call the static method: <code>JsonReader.toMaps(String json)</code>.  This will
 * return an untyped object representation of the JSON String as a Map of Maps, where
 * the fields are the Map keys, and the field values are the associated Map's values.
 * In this representation: All integer numbers are stored as java.lang.Long objects.
 * All floating point numbers are stored as java.lang.Double instances. Booleans are
 * stored as java.lang.Boolean.  All other values are stored as Strings.</li>
 * <li>
 * Instantiate the JsonReader with an InputStream: <code>JsonReader(InputStream in)</code> and then call
 * <code>readObject()</code>.  Cast the return value of readObject() to the Java class that was the root of
 * the graph.
 * </li>
 * <li>
 * Instantiate the JsonReader with an InputStream: <code>JsonReader(InputStream in, true)</code> and then call
 * <code>readObject()</code>.  The return value will be a Map of Maps.
 * </li></ul><br/>
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
public class JsonReader extends Reader
{
    private static final String EMPTY_ARRAY = "~!a~";   // compared with ==
    private static final String EMPTY_OBJECT = "~!o~";  // compared with ==
    private static final Character[] _charCache = new Character[128];
    private static final Byte[] _byteCache = new Byte[256];
    private static final Map<String, String> _stringCache = new HashMap<String, String>();
    private static final Set<Class> _prims = new HashSet<Class>();
    private static final Map<Class, Constructor> _constructors = new HashMap<Class, Constructor>();
    private static final Map<String, Class> _nameToClass = new HashMap<String, Class>();
    private final Map<Object, JsonObject> _objsRead = new IdentityHashMap<Object, JsonObject>();
    private final Collection<UnresolvedReference> _unresolvedRefs = new ArrayList<UnresolvedReference>();
    private final Collection<Map> _maps = new ArrayList<Map>();
    private final Collection<Object[]> _prettyMaps = new ArrayList<Object[]>();
    private final FastPushbackReader _in;
    private boolean _noObjects = false;
    private final char[] _numBuf = new char[256];
    private final StringBuilder _strBuf = new StringBuilder();
    private SimpleDateFormat _dateFormat = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSSZ");

    static
    {
        // Save memory by re-using common Characters (Characters are immutable)
        for (int i = 0; i < _charCache.length; i++)
        {
            _charCache[i] = (char) i;
        }

        // Save memory by re-using all byte instances (Bytes are immutable)
        for (int i = 0; i < _byteCache.length; i++)
        {
            _byteCache[i] = (byte) (i - 128);
        }

        // Save heap memory by re-using common strings (Strings immutable)
        _stringCache.put("", "");
        _stringCache.put("true", "true");
        _stringCache.put("false", "false");
        _stringCache.put("TRUE", "TRUE");
        _stringCache.put("FALSE", "FALSE");
        _stringCache.put("True", "True");
        _stringCache.put("False", "False");
        _stringCache.put("null", "null");
        _stringCache.put("yes", "yes");
        _stringCache.put("no", "no");
        _stringCache.put("YES", "YES");
        _stringCache.put("NO", "NO");
        _stringCache.put("Yes", "Yes");
        _stringCache.put("No", "No");
        _stringCache.put("on", "on");
        _stringCache.put("off", "off");
        _stringCache.put("ON", "ON");
        _stringCache.put("OFF", "OFF");
        _stringCache.put("On", "On");
        _stringCache.put("Off", "Off");
        _stringCache.put("@id", "@id");
        _stringCache.put("@ref", "@ref");
        _stringCache.put("@items", "@items");
        _stringCache.put("@type", "@type");
        _stringCache.put("@keys", "@keys");
        _stringCache.put("0", "0");
        _stringCache.put("1", "1");
        _stringCache.put("2", "2");
        _stringCache.put("3", "3");
        _stringCache.put("4", "4");
        _stringCache.put("5", "5");
        _stringCache.put("6", "6");
        _stringCache.put("7", "7");
        _stringCache.put("8", "8");
        _stringCache.put("9", "9");

        _prims.add(Byte.class);
        _prims.add(Integer.class);
        _prims.add(Long.class);
        _prims.add(Double.class);
        _prims.add(Character.class);
        _prims.add(Float.class);
        _prims.add(Boolean.class);
        _prims.add(Short.class);
        _prims.add(String.class);
        _prims.add(Date.class);
        _prims.add(Class.class);

        _nameToClass.put("string", String.class);
        _nameToClass.put("boolean", boolean.class);
        _nameToClass.put("char", char.class);
        _nameToClass.put("byte", byte.class);
        _nameToClass.put("short", short.class);
        _nameToClass.put("int", int.class);
        _nameToClass.put("long", long.class);
        _nameToClass.put("float", float.class);
        _nameToClass.put("double", double.class);
        _nameToClass.put("date", Date.class);
        _nameToClass.put("class", Class.class);
    }

    private static class JsonArray extends ArrayList { }

    /**
     * LinkedHashMap used to keep fields in same order as they are
     * when reflecting them in Java.  Instances of this class hold a
     * Map-of-Map representation of a Java object, read from the JSON
     * input stream.
     *
     * @param <String> field name in Map-of-Map
     * @param <V>      Value
     */
    private static class JsonObject<String, V> extends LinkedHashMap<String, V>
    {
        private Object target;
    }

    /**
     * UnresolvedReference is created to hold a logical pointer to a reference that
     * could not yet be loaded, as the @ref appears ahead of the referenced object's
     * definition.  This can point to a field reference or an array/Collection element reference.
     */
    private static class UnresolvedReference
    {
        private JsonObject referencingObj;
        private String field;
        private long refId;
        private int index = -1;

        private UnresolvedReference(JsonObject referer, String fld, long id)
        {
            referencingObj = referer;
            field = fld;
            refId = id;
        }

        private UnresolvedReference(JsonObject referer, int idx, long id)
        {
            referencingObj = referer;
            index = idx;
            refId = id;
        }
    }

    /**
     * Convert the passed in JSON string into a Java object graph.
     *
     * @param json String JSON input
     * @return Java object graph matching JSON input, or null if an
     *         error occurred.
     */
    public static Object toJava(String json)
    {
        try
        {
            return jsonToJava(json);
        }
        catch (Exception ignored)
        {
            return null;
        }
    }

    /**
     * Convert the passed in JSON string into a Java object graph.
     *
     * @param json String JSON input
     * @return Java object graph matching JSON input
     * @throws java.io.IOException If an I/O error occurs
     */
    public static Object jsonToJava(String json) throws IOException
    {
        ByteArrayInputStream ba = new ByteArrayInputStream(json.getBytes("UTF-8"));
        JsonReader jr = new JsonReader(ba, false);
        return jr.readObject();
    }

    /**
     * Convert the passed in JSON string into a Java object graph
     * that consists solely of Java Maps where the keys are the
     * fields and the values are primitives or other Maps (in the
     * case of objects).
     *
     * @param json String JSON input
     * @return Java object graph of Maps matching JSON input,
     *         or null if an error occurred.
     */
    public static Map toMaps(String json)
    {
        try
        {
            return jsonToMaps(json);
        }
        catch (Exception ignored)
        {
            return null;
        }
    }

    /**
     * Convert the passed in JSON string into a Java object graph
     * that consists solely of Java Maps where the keys are the
     * fields and the values are primitives or other Maps (in the
     * case of objects).
     *
     * @param json String JSON input
     * @return Java object graph of Maps matching JSON input,
     *         or null if an error occurred.
     * @throws java.io.IOException If an I/O error occurs
     */
    public static Map jsonToMaps(String json) throws IOException
    {
        ByteArrayInputStream ba = new ByteArrayInputStream(json.getBytes("UTF-8"));
        JsonReader jr = new JsonReader(ba, true);
        return (Map) jr.readObject();
    }

    public JsonReader(InputStream in)
    {
        this(in, false);
    }

    public JsonReader(InputStream in, boolean noObjects)
    {
        _noObjects = noObjects;
        try
        {
            _in = new FastPushbackReader(new BufferedReader(new InputStreamReader(in, "UTF-8")));
        }
        catch (UnsupportedEncodingException e)
        {
            throw new RuntimeException("Your JVM does not support UTF-8.  Get a new JVM.", e);
        }
    }

    /**
     * Finite State Machine (FSM) used to parse the JSON input.
     *
     * @return Java Object graph constructed from InputStream supplying
     *         JSON serialized content.
     * @throws IOException for stream errors or parsing errors.
     */
    public Object readObject() throws IOException
    {
        Object o = readIntoJsonMaps();
        if (o == EMPTY_ARRAY)
        {
            return new Object[]{};
        }
        else if (o == EMPTY_OBJECT)
        {
            return null;
        }

        // Allow a complete 'Map' return (Javascript style)
        if (_noObjects)
        {
            return o;
        }

        createJavaObjectInstance(Object.class, (JsonObject) o);
        Object graph = convertMapsToObjects((JsonObject<String, Object>) o);
        patchUnresolvedReferences();
        _objsRead.clear();
        _unresolvedRefs.clear();
        _maps.clear();
        _prettyMaps.clear();
        return graph;
    }

    /**
     * Walk a JsonObject (Map of String keys to values) and return the
     * Java object equivalent filled in as best as possible (everything
     * except unresolved reference fields or unresolved array/collection elements).
     *
     * @param root JsonObject reference to a Map-of-Maps representation of the JSON
     *             input after it has been completely read.
     * @return Properly constructed, typed, Java object graph built from a Map
     *         of Maps representation (JsonObject root).
     * @throws IOException for stream errors or parsing errors.
     */
    private Object convertMapsToObjects(JsonObject<String, Object> root) throws IOException
    {
        LinkedList<JsonObject<String, Object>> stack = new LinkedList<JsonObject<String, Object>>();
        stack.addFirst(root);

        while (!stack.isEmpty())
        {
            JsonObject<String, Object> jsonObj = stack.removeFirst();
            final Object javaMate = jsonObj.target;

            if (javaMate.getClass().isArray())
            {    // Handle assigning Map to javaMate that is an []
                traverseArray(stack, jsonObj);
            }
            else if ((javaMate instanceof Collection) && jsonObj.containsKey("@items"))
            {
                traverseCollection(stack, jsonObj);
            }
            else if (javaMate instanceof Map && jsonObj.containsKey("@keys") && jsonObj.containsKey("@items"))
            {
                traverseMap(stack, jsonObj);
            }
            else
            {
                if (javaMate instanceof Map)
                {
                    _maps.add((Map) javaMate);      // List of maps to be rebuilt (re-indexed)
                }
                traverseFields(stack, jsonObj);
            }
        }
        return root.target;
    }

    /**
     * Traverse the JsonObject associated to an array (of any type).  Convert and
     * assign the list of items in the JsonObject (stored in the @items field)
     * to each array element.  All array elements are processed excluding elements
     * that reference an unresolved object.  These are filled in later.
     *
     * @param stack   a Stack (LinkedList) used to support graph traversal.
     * @param jsonObj a Map-of-Map representation of the JSON input stream.
     * @throws IOException for stream errors or parsing errors.
     */
    private void traverseArray(LinkedList<JsonObject<String, Object>> stack, JsonObject<String, Object> jsonObj) throws IOException
    {
        Object array = jsonObj.target;
        int len = Array.getLength(array);
        if (len == 0)
        {
            return;
        }
        JsonArray items = (JsonArray) jsonObj.get("@items");
        Class compType = array.getClass().getComponentType();

        if (byte.class.equals(compType))
        {   // Handle byte[] special for performance boost.
            byte[] bytes = (byte[]) array;
            for (int i = 0; i < len; i++)
            {
                bytes[i] = ((Number) items.get(i)).byteValue();
            }
            return;
        }

        boolean isPrimitive = isPrimitive(compType);

        for (int i = 0; i < len; i++)
        {
            Object element = items.get(i);

            if (element == null)
            {
                Array.set(array, i, null);
            }
            else if (element == EMPTY_OBJECT)
            {    // Use either explicitly defined type in ObjectMap associated to JSON, or array component type.
                Object arrayElement = createJavaObjectInstance(compType, new JsonObject());
                Array.set(array, i, arrayElement);
            }
            else if (element instanceof JsonArray)
            {   // Array of arrays
                if (char[].class.equals(compType))
                {   // Specially handle char[] because we are writing these
                    // out as UTF-8 strings for compactness and speed.
                    JsonArray jsonArray = (JsonArray) element;
                    if (jsonArray.size() == 0)
                    {
                        Array.set(array, i, new char[]{});
                    }
                    else
                    {
                        String value = (String) jsonArray.get(0);
                        int numChars = value.length();
                        char[] chars = new char[numChars];
                        for (int j = 0; j < numChars; j++)
                        {
                            chars[j] = value.charAt(j);
                        }
                        Array.set(array, i, chars);
                    }
                }
                else
                {
                    JsonObject<String, Object> jsonObject = new JsonObject<String, Object>();
                    jsonObject.put("@items", element);
                    Array.set(array, i, createJavaObjectInstance(compType, jsonObject));
                    stack.addFirst(jsonObject);
                }
            }
            else if (element instanceof JsonObject)
            {
                JsonObject<String, Object> jsonObject = (JsonObject<String, Object>) element;
                Long ref = (Long) jsonObject.get("@ref");

                if (ref != null)
                {    // Connect reference
                    JsonObject refObject = _objsRead.get(ref);
                    if (refObject != null && refObject.target != null)
                    {   // Array element with @ref to existing object
                        Array.set(array, i, refObject.target);
                    }
                    else
                    {    // Array with a forward @ref as an element
                        _unresolvedRefs.add(new UnresolvedReference(jsonObj, i, ref));
                    }
                }
                else
                {    // Convert JSON HashMap to Java Object instance and assign values
                    Object arrayElement = createJavaObjectInstance(compType, jsonObject);
                    Array.set(array, i, arrayElement);
                    if (!isPrimitive(arrayElement.getClass()))
                    {    // Skip walking primitives, primitive wrapper classes, Dates, Strings, and Classes
                        stack.addFirst(jsonObject);
                    }
                }
            }
            else if (isPrimitive)
            {   // Primitive component type array
                Array.set(array, i, newPrimitiveWrapper(compType, element));
            }
            else
            {   // Setting primitive values into an Object[]
                Array.set(array, i, element);
            }
        }
    }

    /**
     * Process java.util.Collection and it's derivatives.  Collections are written specially
     * so that the serialization does not expose the Collection's internal structure, for
     * example a TreeSet.  All entries are processed, except unresolved references, which
     * are filled in later.  For an indexable collection, the unresolved references are set
     * back into the proper element location.  For non-indexable collections (Sets), the
     * unresolved references are added via .add().
     */
    private void traverseCollection(LinkedList<JsonObject<String, Object>> stack, JsonObject jsonObj) throws IOException
    {
        JsonArray items = (JsonArray) jsonObj.get("@items");

        if (items == null)
        {
            return;
        }

        Collection col = (Collection) jsonObj.target;
        Iterator i = items.iterator();
        boolean isList = col instanceof List;
        int idx = 0;

        while (i.hasNext())
        {
            Object element = i.next();

            if (element == null)
            {
                col.add(null);
            }
            else if (element == EMPTY_OBJECT)
            {    // Use either explicitly defined type in ObjectMap associated to JSON, or array component type.
                col.add(createJavaObjectInstance(Object.class, jsonObj));
            }
            else if (element instanceof String || element instanceof Boolean || element instanceof Double || element instanceof Long)
            {    // Allow Strings, Booleans, Longs, and Doubles  to be "inline" without Java object decoration (@id, @type, etc.)
                col.add(element);
            }
            else if (element instanceof JsonArray)
            {
                JsonObject jObj = new JsonObject();
                jObj.put("@items", element);
                createJavaObjectInstance(Object.class, jObj);
                col.add(jObj.target);
                convertMapsToObjects(jObj);
            }
            else if (element instanceof JsonObject)
            {
                JsonObject jObj = (JsonObject) element;
                Long ref = (Long) jObj.get("@ref");

                if (ref != null)
                {
                    JsonObject refObject = _objsRead.get(ref);
                    if (refObject != null && refObject.target != null)
                    {
                        col.add(refObject.target);
                    }
                    else
                    {
                        _unresolvedRefs.add(new UnresolvedReference(jsonObj, idx, ref));
                        if (isList)
                        {   // Indexable collection, so set 'null' as element for now - will be patched in later.
                            col.add(null);
                        }
                    }
                }
                else
                {
                    createJavaObjectInstance(Object.class, jObj);

                    if (!isPrimitive(jObj.target.getClass()))
                    {
                        convertMapsToObjects(jObj);
                    }
                    col.add(jObj.target);
                }
            }
            else
            {
                throw new IOException("Encountered unknown type - " + element.getClass().getName());
            }
            idx++;
        }
    }

    /**
     * Process java.util.Map and it's derivatives.  These can be written specially
     * so that the serialization would not expose the derivative class internals
     * (internal fields of TreeMap for example).
     */
    private void traverseMap(LinkedList<JsonObject<String, Object>> stack, JsonObject jsonObj) throws IOException
    {
        // Convert @keys to a Collection of Java objects.
        JsonArray keys = (JsonArray) jsonObj.get("@keys");
        JsonArray items = (JsonArray) jsonObj.get("@items");

        if (keys == null || items == null)
        {
            if (keys != items)
            {
                throw new IOException("Map written where one of @keys or @items is empty, jsonObject:\n" + jsonObj);
            }
            return;
        }

        JsonObject jsonKeyCollection = new JsonObject();
        jsonKeyCollection.put("@items", keys);
        Collection javaKeys = new ArrayList();
        jsonKeyCollection.target = javaKeys;
        stack.addFirst(jsonKeyCollection);

        // Convert @items to a Collection of Java objects.
        JsonObject jsonItemCollection = new JsonObject();
        jsonItemCollection.put("@items", items);
        Collection javaValues = new ArrayList();
        jsonItemCollection.target = javaValues;
        stack.addFirst(jsonItemCollection);

        if (javaKeys.size() != javaValues.size())
        {
            throw new IOException("Map written with @keys and @items entries of different sizes, jsonObject:\n" + jsonObj);
        }

        // Save these for later so that unresolved references inside keys or values
        // get patched first, and then build the Maps.
        _prettyMaps.add(new Object[]{jsonObj.target, javaKeys, javaValues});
    }

    private void traverseFields(LinkedList<JsonObject<String, Object>> stack, JsonObject<String, Object> jsonObj) throws IOException
    {
        Object javaMate = jsonObj.target;

        JsonWriter.ClassMeta meta = JsonWriter.getDeepDeclaredFields(javaMate.getClass());

        if (meta._readMethod != null)
        {
            try
            {
                meta._readMethod.invoke(javaMate, jsonObj);
            }
            catch (InvocationTargetException e)
            {
                throw new IOException("Unabled to invoke " + javaMate.getClass().getName() + "." + meta._readMethod.getName(), e.getTargetException());
            }
            catch (IllegalAccessException e)
            {
                throw new IOException("Unabled to invoke " + javaMate.getClass().getName() + "." + meta._readMethod.getName(), e);
            }
        }
        else
        {
            Iterator<Map.Entry<String, Object>> i = jsonObj.entrySet().iterator();
            Class cls = javaMate.getClass();

            while (i.hasNext())
            {
                Map.Entry<String, Object> e = i.next();
                String key = e.getKey();

                if (key.charAt(0) == '@')
                {   // Skip our own generated fields
                    continue;
                }

                Field field = getDeclaredField(cls, key);

                if (field != null)
                {
                    assignField(stack, jsonObj, field, e.getValue());
                }
            }
        }
    }

    /**
     * Map Json Map object field to Java object field.
     *
     * @param stack   Stack (LinkedList) used for graph traversal.
     * @param jsonObj a Map-of-Map representation of the current object being examined (containing all fields).
     * @param field   a Java Field object representing where the jsonObj should be converted and stored.
     * @param rhs     the JSON value that will be converted and stored in the 'field' on the associated
     *                Java target object.
     * @throws IOException for stream errors or parsing errors.
     */
    private void assignField(LinkedList<JsonObject<String, Object>> stack, JsonObject jsonObj, Field field, Object rhs) throws IOException
    {
        Object target = jsonObj.target;
        try
        {
            Class fieldType = field.getType();

            if (rhs == null)
            {
                field.set(target, null);
            }
            else if (rhs == EMPTY_OBJECT)
            {
                field.set(target, newInstance(fieldType));
            }
            else if (rhs instanceof JsonArray)
            {    // LHS of assignment is an [] field OR right handside is an array and LHS is Object
                JsonArray elements = (JsonArray) rhs;
                JsonObject<String, Object> jsonArray = new JsonObject<String, Object>();
                if (char[].class.equals(fieldType))
                {   // Specially handle char[] because we are writing these
                    // out as UTF8 strings for compactness and speed.
                    if (elements.size() == 0)
                    {
                        field.set(target, new char[]{});
                    }
                    else
                    {
                        field.set(target, ((String) elements.get(0)).toCharArray());
                    }
                }
                else
                {
                    jsonArray.put("@items", elements);
                    createJavaObjectInstance(fieldType, jsonArray);
                    field.set(target, jsonArray.target);
                    stack.addFirst(jsonArray);
                }
            }
            else if (rhs instanceof JsonObject)
            {
                JsonObject<String, Object> jObj = (JsonObject) rhs;
                Long ref = (Long) jObj.get("@ref");

                if (ref != null)
                {    // Correct field references
                    JsonObject refObject = _objsRead.get(ref);
                    if (refObject != null && refObject.target != null)
                    {
                        field.set(target, refObject.target);
                    }
                    else
                    {
                        _unresolvedRefs.add(new UnresolvedReference(jsonObj, field.getName(), ref));
                    }
                }
                else
                {    // Assign ObjectMap's to Object (or derived) fields
                    field.set(target, createJavaObjectInstance(fieldType, jObj));
                    if (!isPrimitive(jObj.target.getClass()))
                    {
                        stack.addFirst((JsonObject) rhs);
                    }
                }
            }
            else if (isPrimitive(fieldType))
            {    // field.set() will convert primitive wrapper objects to primitives for assignment to member
                field.set(target, newPrimitiveWrapper(fieldType, rhs));
            }
            else
            {
                // Object field type with a primitive RHS
                field.set(target, rhs);
            }
        }
        catch (IllegalAccessException e)
        {
            throw new IOException("IllegalAccessException setting field '" + field.getName() + "' on target: " + target + "\nwith value: " + rhs);
        }
    }

    /**
     * This method creates a Java Object instance based on the passed in parameters.
     * If the JsonObject contains a key '@type' then that is used, as the type was explicitly
     * set in the JSON stream.  If the key '@type' does not exist, then the passed in Class
     * is used to create the instance, handling creating an Array or regular Object
     * instance.
     * <p/>
     * The '@type' is not usually specified that much in the JSON input stream, as in
     * many cases it can be inferred from a field reference or array component type.
     *
     * @param clazz   Instance will be create of this class.
     * @param jsonObj Map-of-Map representation of object to create.
     * @return a new Java object of the appropriate type (clazz) using the jsonObj to provide
     *         enough hints to get the right class instantiated.  It is not populated when returned.
     * @throws IOException for stream errors or parsing errors.
     */
    private Object createJavaObjectInstance(Class clazz, JsonObject jsonObj) throws IOException
    {
        String type = (String) jsonObj.get("@type");
        Object mate;

        // @type always takes precedence over inferred Java (clazz) type.
        if (type != null)
        {    // @type is explicitly set, use that as it always takes precedence
            Class c = classForName(type);
            if (c.isArray())
            {    // Handle []
                JsonArray items = (JsonArray) jsonObj.get("@items");
                if (items == null)
                {
                    throw new IOException("Array [] specified with no @items");
                }
                mate = Array.newInstance(c.getComponentType(), items.size());
            }
            else
            {    // Handle regular field.object reference
                if (isPrimitive(c))
                {
                    mate = newPrimitiveWrapper(c, jsonObj.get("value"));
                }
                else
                {
                    mate = newInstance(c);
                }
            }
        }
        else
        {    // @type, not specified, figure out appropriate type
            JsonArray items = (JsonArray) jsonObj.get("@items");

            // if @items is specified, it must be an [] type.
            // if clazz.isArray(), then it must be an [] type.
            if (clazz.isArray() || (items != null && clazz.equals(Object.class)))
            {
                if (items == null)
                {
                    throw new IOException("'@items' not specified for a JSON array, no way to determine length");
                }

                mate = Array.newInstance(clazz.isArray() ? clazz.getComponentType() : Object.class, items.size());
            }
            else
            {    // Definitely not an [] type.
                if (isPrimitive(clazz))
                {    // If it is a primitive wrapper, String, Date, or Class
                    mate = newPrimitiveWrapper(clazz, jsonObj.get("value"));
                }
                else if (clazz.equals(Object.class) && jsonObj.containsKey("value"))
                {
                    mate = jsonObj.get("value");
                }
                else
                {    // Use the passed in class type
                    mate = newInstance(clazz);
                }
            }
        }
        jsonObj.target = mate;
        return mate;
    }

    // Parser code

    private Object readIntoJsonMaps() throws IOException
    {
        final int STATE_READ_START_OBJECT = 0;
        final int STATE_READ_FIELD = 1;
        final int STATE_READ_VALUE = 2;
        final int STATE_READ_POST_VALUE = 3;
        boolean done = false;
        String field = null;
        JsonObject<String, Object> object = new JsonObject<String, Object>();
        int state = STATE_READ_START_OBJECT;

        while (!done)
        {
            int c;
            switch (state)
            {
                case STATE_READ_START_OBJECT:
                    c = skipWhitespaceRead();
                    if (c == '{')
                    {
                        c = skipWhitespaceRead();
                        if (c == '}')
                        {    // empty object
                            return EMPTY_OBJECT;
                        }
                        _in.unread(c);
                        state = STATE_READ_FIELD;
                    }
                    else if (c == '[')
                    {
                        _in.unread('[');
                        state = STATE_READ_VALUE;
                    }
                    else
                    {
                        throw new IOException("Input is not valid JSON; does not start with '{' or '['");
                    }
                    break;

                case STATE_READ_FIELD:
                    c = skipWhitespaceRead();
                    if (c == '"')
                    {
                        field = readString();
                        c = skipWhitespaceRead();
                        if (c != ':')
                        {
                            throw new IOException("Expected ':' between string field and value at position " + _in.getPos());
                        }
                        skipWhitespace();
                        state = STATE_READ_VALUE;
                    }
                    else
                    {
                        throw new IOException("Expected quote at position " + _in.getPos());
                    }
                    break;

                case STATE_READ_VALUE:
                    c = skipWhitespaceRead();
                    if (c == '[')
                    {
                        Object o = readArray();
                        if (field == null)
                        {    // field is null when you have an untyped Object[], so we place
                            // the JsonArray on the @items field.
                            field = "@items";
                        }
                        object.put(field, o);
                        if (o != EMPTY_ARRAY)
                        {
                            // If object is referenced (has @id), then put it in the _objsRead table.
                            if ("@id".equals(field))
                            {
                                _objsRead.put(o, object);
                            }
                        }
                        state = STATE_READ_POST_VALUE;
                    }
                    else
                    {
                        _in.unread(c);
                        Object o = readValue();
                        object.put(field, o);
                        // If object is referenced (has @id), then put it in the _objsRead table.
                        if ("@id".equals(field))
                        {
                            _objsRead.put(o, object);
                        }
                        state = STATE_READ_POST_VALUE;
                    }
                    break;

                case STATE_READ_POST_VALUE:
                    c = skipWhitespaceRead();
                    if (c == '}' || c == -1)
                    {
                        done = true;
                    }
                    else if (c == ',')
                    {
                        state = STATE_READ_FIELD;
                    }
                    else
                    {
                        throw new IOException("Object not ended with '}' at position " + _in.getPos());
                    }
                    break;
            }
        }

        return object;
    }

    /**
     * Read non-Array value
     *
     * @return Object that represents a JSON value.  See JSON specification for
     *         exactly what a 'value' can be.
     * @throws IOException for stream errors or parsing errors.
     */
    private Object readValue() throws IOException
    {
        int c = _in.read();

        if (isDigit(c) || c == '-')
        {
            return readNumber(c);
        }
        else if (c == '"')
        {
            return readString();
        }
        else if (c == '{')
        {
            _in.unread('{');
            return readIntoJsonMaps();
        }
        else if (c == 'n' || c == 'N')
        {
            _in.unread(c);
            readToken("null");
            return null;
        }
        else if (c == 't' || c == 'T')
        {
            _in.unread(c);
            readToken("true");
            return Boolean.TRUE;
        }
        else if (c == 'f' || c == 'F')
        {
            _in.unread(c);
            readToken("false");
            return Boolean.FALSE;
        }
        else
        {
            throw new IOException("Unknown value type at position " + _in.getPos());
        }
    }

    /**
     * Read a JSON array
     *
     * @return a JsonArray (ArrayList) containing the parsed items from the
     *         [] in JSON.  These will be stored in simple forms like Double, Boolean,
     *         null, Long, String, or JsonObject for non-primitive types.
     * @throws IOException for stream errors or parsing errors.
     */
    private Object readArray() throws IOException
    {
        LinkedList<JsonArray> stack = new LinkedList<JsonArray>();
        JsonArray root = new JsonArray();
        stack.addFirst(root);

        while (!stack.isEmpty())
        {
            JsonArray array = stack.removeFirst();

            while (true)
            {
                skipWhitespace();
                int c = _in.read();
                if (c == ']')
                {
                    break;
                }
                else if (c == ',')
                {
                }
                else if (c == '[')
                {   // read a nested array
                    stack.addFirst(array);
                    JsonArray newArray = new JsonArray();
                    array.add(newArray);
                    stack.addFirst(newArray);
                    break;
                }
                else
                {   // read a value (any value but array)
                    _in.unread(c);
                    Object o = readValue();
                    if (o != EMPTY_ARRAY)
                    {
                        array.add(o);
                    }
                }
            }
        }

        return root;
    }

    /**
     * Return the specified token from the reader.  If it is not found,
     * throw an IOException indicating that.  Converting to c to
     * (char) c is acceptable because the 'tokens' allowed in a
     * JSON input stream (true, false, null) are all ASCII.
     */
    private String readToken(String token) throws IOException
    {
        int len = token.length();

        for (int i = 0; i < len; i++)
        {
            int c = _in.read();
            c = Character.toLowerCase((char) c);
            int loTokenChar = token.charAt(i);

            if (loTokenChar != c)
            {
                throw new IOException("Expected token '" + token + "' at position " + _in.getPos());
            }
        }

        return token;
    }

    /**
     * Read a JSON number
     *
     * @param c int a character representing the first digit of the number that
     *          was already read.
     * @return a Number (a Long or a Double) depending on whether the number is
     *         a decimal number or integer.  This choice allows all smaller types (Float, int, short, byte)
     *         to be represented as well.
     * @throws IOException for stream errors or parsing errors.
     */
    private Number readNumber(int c) throws IOException
    {
        _numBuf[0] = (char) c;
        int len = 1;
        boolean isFloat = false;

        try
        {
            while (true)
            {
                c = _in.read();
                if (isDigit(c) || c == '-' || c == '+')
                {
                    _numBuf[len++] = (char) c;
                }
                else if (c == '.' || c == 'e' || c == 'E')
                {
                    _numBuf[len++] = (char) c;
                    isFloat = true;
                }
                else
                {
                    _in.unread(c);
                    break;
                }
            }
        }
        catch (ArrayIndexOutOfBoundsException e)
        {
            throw new IOException("Too many digits in number at position " + _in.getPos());
        }

        if (isFloat)
        {   // Floating point number needed
            String num = new String(_numBuf, 0, len);
            try
            {
                return Double.parseDouble(num);
            }
            catch (NumberFormatException e)
            {
                throw new IOException("Invalid floating point number at position " + _in.getPos() + ", number: " + num);
            }
        }
        else
        {
            boolean isNeg = _numBuf[0] == '-';
            long n = 0;
            for (int i = (isNeg ? 1 : 0); i < len; i++)
            {
                n = (_numBuf[i] - '0') + n * 10;
            }
            return isNeg ? -n : n;
        }
    }

    /**
     * Read a JSON string
     * This method assumes the initial quote has already been read.
     *
     * @return String read from JSON input stream.
     * @throws IOException for stream errors or parsing errors.
     */
    private String readString() throws IOException
    {
        _strBuf.setLength(0);
        StringBuilder hex = new StringBuilder();
        boolean done = false;
        final int STATE_STRING_START = 0;
        final int STATE_STRING_SLASH = 1;
        final int STATE_HEX_DIGITS = 2;
        final int STATE_STRING_END = 3;
        int state = STATE_STRING_START;

        while (!done)
        {
            int c = _in.read();

            switch (state)
            {
                case STATE_STRING_START:
                    if (c == '\\')
                    {
                        state = STATE_STRING_SLASH;
                    }
                    else if (c == '"')
                    {
                        state = STATE_STRING_END;
                        done = true;
                    }
                    else if (c == -1)
                    {
                        throw new IOException("End of input reached before expected String end quote (\")");
                    }
                    else
                    {
                        _strBuf.append(toChars(c));
                    }
                    break;

                case STATE_STRING_SLASH:
                    if (c == 'n')
                    {
                        _strBuf.append('\n');
                    }
                    else if (c == 'r')
                    {
                        _strBuf.append('\r');
                    }
                    else if (c == 't')
                    {
                        _strBuf.append('\t');
                    }
                    else if (c == 'f')
                    {
                        _strBuf.append('\f');
                    }
                    else if (c == 'b')
                    {
                        _strBuf.append('\b');
                    }
                    else if (c == '\\')
                    {
                        _strBuf.append('\\');
                    }
                    else if (c == '/')
                    {
                        _strBuf.append('/');
                    }
                    else if (c == '"')
                    {
                        _strBuf.append('"');
                    }
                    else if (c == 'u')
                    {
                        state = STATE_HEX_DIGITS;
                        hex.setLength(0);
                        break;
                    }
                    else
                    {
                        throw new IOException("Invalid character escape sequence specified at position " + _in.getPos());
                    }
                    state = STATE_STRING_START;
                    break;

                case STATE_HEX_DIGITS:
                    if (c == 'a' || c == 'A' || c == 'b' || c == 'B' || c == 'c' || c == 'C' || c == 'd' || c == 'D' || c == 'e' || c == 'E' || c == 'f' || c == 'F' || isDigit(c))
                    {
                        hex.append((char) c);
                        if (hex.length() == 4)
                        {
                            int value = Integer.parseInt(hex.toString(), 16);
                            _strBuf.append(valueOf((char) value));
                            state = STATE_STRING_START;
                        }
                    }
                    else if (c == -1)
                    {
                        throw new IOException("End of input reached before hex number read at position " + _in.getPos());
                    }
                    else
                    {
                        throw new IOException("Expected hexidecimal digits at position " + _in.getPos());
                    }
                    break;

                case STATE_STRING_END:
                    done = true;
                    break;
            }
        }

        String s = _strBuf.toString();
        String cacheHit = _stringCache.get(s);
        if (cacheHit == null)
        {
            return s;
        }
        return cacheHit;
    }

    private Object newInstance(Class c) throws IOException
    {
        // See if constructor cached first
        Constructor constructor = _constructors.get(c);
        if (constructor != null)
        {
            Class[] paramTypes = constructor.getParameterTypes();
            if (paramTypes == null || paramTypes.length == 0)
            {
                try
                {
                    return constructor.newInstance();
                }
                catch (Exception e)
                {
                    throw new IOException("Could not instantiate " + c.getName());
                }
            }
            else
            {
                Object[] values = fillArgs(paramTypes);
                try
                {
                    return constructor.newInstance(values);
                }
                catch (Exception e)
                {
                    throw new IOException("Could not instantiate " + c.getName());
                }

            }
        }
        Object[] ret = newInstanceEx(c);
        _constructors.put(c, (Constructor) ret[0]);
        return ret[1];
    }

    /**
     * Return constructor and instance as elements 0 and 1, respectively.
     */
    private Object[] newInstanceEx(Class c) throws IOException
    {
        try
        {
            Constructor constructor = c.getConstructor(null);
            if (constructor != null)
            {
                return new Object[] {constructor, constructor.newInstance()};
            }
            return tryOtherConstructors(c);
        }
        catch (Exception e)
        {
            // OK, this class does not have a public no-arg constructor.  Instantiate with
            // first constructor found, filling in constructor values with null or
            // defaults for primitives.
            return tryOtherConstructors(c);
        }
    }

    private Object[] tryOtherConstructors(Class c) throws IOException
    {
        Constructor[] constructors = c.getDeclaredConstructors();
        if (constructors.length == 0)
        {
            throw new IOException("Cannot instantiate '" + c.getName() + "' - Primitive, interface, array[] or void at position " + _in.getPos());
        }

        // Try each constructor (private, protected, or public) with default values until
        // the object instantiates without exception.
        for (Constructor constructor : constructors)
        {
            constructor.setAccessible(true);
            Class[] argTypes = constructor.getParameterTypes();
            Object[] values = fillArgs(argTypes);
            try
            {
                return new Object[] {constructor, constructor.newInstance(values)};
            }
            catch (Exception ignored)
            { }
        }

        throw new IOException("Could not instantiate " + c.getName() + " using any constructor");
    }

    private static Object[] fillArgs(Class[] argTypes)
    {
        Object[] values = new Object[argTypes.length];
        for (int i = 0; i < argTypes.length; i++)
        {
            if (argTypes[i].isPrimitive())
            {
                if (argTypes[i].equals(byte.class))
                {
                    values[i] = (byte) 0;
                }
                else if (argTypes[i].equals(short.class))
                {
                    values[i] = (short) 0;
                }
                else if (argTypes[i].equals(int.class))
                {
                    values[i] = 0;
                }
                else if (argTypes[i].equals(long.class))
                {
                    values[i] = 0L;
                }
                else if (argTypes[i].equals(boolean.class))
                {
                    values[i] = Boolean.FALSE;
                }
                else if (argTypes[i].equals(float.class))
                {
                    values[i] = 0.0f;
                }
                else if (argTypes[i].equals(double.class))
                {
                    values[i] = 0.0;
                }
                else if (argTypes[i].equals(char.class))
                {
                    values[i] = (char) 0;
                }
            }
            else
            {
                values[i] = null;
            }
        }

        return values;
    }

    private static boolean isPrimitive(Class c)
    {
        return c.isPrimitive() || _prims.contains(c) ||
                Calendar.class.isAssignableFrom(c) || BigDecimal.class.isAssignableFrom(c) ||
                BigInteger.class.isAssignableFrom(c) || java.sql.Date.class.isAssignableFrom(c);
    }

    private Object newPrimitiveWrapper(Class c, Object rhs) throws IOException
    {
        if (c.equals(Byte.class) || c.equals(byte.class))
        {
            return _byteCache[((Number) rhs).byteValue() + 128];
        }
        else if (c.equals(String.class))
        {
            String cache = _stringCache.get(rhs);
            return cache != null ? cache : rhs;
        }
        else if (c.equals(Boolean.class) || c.equals(boolean.class))
        {    // Booleans are tokenized into Boolean.TRUE or Boolean.FALSE
            return rhs;
        }
        else if (c.equals(Integer.class) || c.equals(int.class))
        {
            return ((Number) rhs).intValue();
        }
        else if (c.equals(Long.class) || c.equals(long.class))
        {
            return rhs;
        }
        else if (c.equals(Double.class) || c.equals(double.class))
        {
            return rhs;
        }
        else if (c.equals(Character.class) || c.equals(char.class))
        {
            return valueOf(((String) rhs).charAt(0));
        }
        else if (c.equals(Short.class) || c.equals(short.class))
        {
            return ((Number) rhs).shortValue();
        }
        else if (c.equals(Float.class) || c.equals(float.class))
        {
            return ((Number) rhs).floatValue();
        }
        else if (c.equals(Date.class))
        {
            return new Date((Long) rhs);
        }
        else if (c.equals(Class.class))
        {
            return classForName((String) rhs);
        }
        else if (Calendar.class.isAssignableFrom(c))
        {
            try
            {
                Date date = _dateFormat.parse((String) rhs);
                Calendar calendar = (Calendar) c.newInstance();
                calendar.setTime(date);

                try
                {   // If time zone is specified at the end in (EST), then place this calendar
                    // in that timezone, without shifting the absolute time.
                    String dateStr = (String) rhs;
                    int idx = dateStr.indexOf('(');
                    if (idx == -1)
                    {
                        return calendar;
                    }
                    String tz = dateStr.substring(idx + 1, dateStr.length() - 1);
                    calendar.setTimeZone(TimeZone.getTimeZone(tz));
                }
                catch (Exception ignored)
                { }
                return calendar;
            }
            catch (Exception e)
            {
                throw new IOException("Failed to parse date: " + rhs);
            }
        }
        else if (BigDecimal.class.isAssignableFrom(c))
        {
            return new BigDecimal((String) rhs);
        }
        else if (BigInteger.class.isAssignableFrom(c))
        {
            return new BigInteger((String)rhs);
        }
        else if (java.sql.Date.class.isAssignableFrom(c))
        {
            return java.sql.Date.valueOf((String)rhs);
        }

        throw new IOException("Class '" + c.getName() + "' requested for special instantiation.");
    }

    private static boolean isDigit(int c)
    {
        return c >= '0' && c <= '9';
    }

    private static boolean isWhitespace(int c)
    {
        return c == ' ' || c == '\t' || c == '\n' || c == '\r';
    }

    private Class classForName(String name) throws IOException
    {
        if (name == null || name.length() == 0)
        {
            throw new IOException("Invalid class name specified, position: " + _in.getPos());
        }
        try
        {
            Class c = _nameToClass.get(name);
            if (c != null)
            {
                return c;
            }
            else
            {
                try
                {
                    return Class.forName(name);
                }
                catch (NullPointerException e)
                {
                    throw new RuntimeException("Could not get class");
                }
            }
        }
        catch (ClassNotFoundException e)
        {
            throw new IOException("Class instance '" + name + "' could not be created at position " + _in.getPos());
        }
    }

    /**
     * Get a Field object using a String field name and a Class instance.  This
     * method will start on the Class passed in, and if not found there, will
     * walk up super classes until it finds the field, or throws an IOException
     * if it cannot find the field.
     *
     * @param c Class containing the desired field.
     * @param fieldName String name of the desired field.
     * @return Field object obtained from the passed in class (by name).  The Field
     *         returned is cached so that it is only obtained via reflection once.
     * @throws IOException for stream errors or parsing errors.
     */
    private Field getDeclaredField(Class c, String fieldName) throws IOException
    {
        JsonWriter.ClassMeta meta = JsonWriter.getDeepDeclaredFields(c);
        return meta.get(fieldName);
    }

    /**
     * Read until non-whitespace character and then return it.
     * This saves extra read/pushback.
     *
     * @return int repesenting the next non-whitespace character in the stream.
     * @throws IOException for stream errors or parsing errors.
     */
    private int skipWhitespaceRead() throws IOException
    {
        int c = _in.read();
        while (isWhitespace(c))
        {
            c = _in.read();
        }

        return c;
    }

    private void skipWhitespace() throws IOException
    {
        int c = skipWhitespaceRead();
        _in.unread(c);
    }

    public void close()
    {
        try
        {
            if (_in != null)
            {
                _in.close();
            }
        }
        catch (IOException ignored)
        {
        }
    }

    /**
     * For all fields where the value was "@ref":"n" where 'n' was the id of an object
     * that had not yet been encountered in the stream, make the final substition.
     * @throws IOException
     */
    private void patchUnresolvedReferences() throws IOException
    {
        Iterator i = _unresolvedRefs.iterator();
        while (i.hasNext())
        {
            UnresolvedReference ref = (UnresolvedReference) i.next();
            Object objToFix = ref.referencingObj.target;
            JsonObject objReferenced = _objsRead.get(ref.refId);

            if (objReferenced == null)
            {
                System.err.println("Back reference (" + ref.refId + ") does not match any object id in input, field '" + ref.field + '\'');
                continue;
            }

            if (objReferenced.target == null)
            {
                System.err.println("Back referenced object does not exist,  @ref " + ref.refId + ", field '" + ref.field + '\'');
                continue;
            }

            if (objToFix == null)
            {
                System.err.println("Referencing object is null, back reference, @ref " + ref.refId + ", field '" + ref.field + '\'');
                continue;
            }

            if (ref.index >= 0)
            {    // Fix []'s and Collections containing a forward reference.
                if (objToFix instanceof List)
                {   // Patch up Indexable Collections
                    List list = (List) objToFix;
                    list.set(ref.index, objReferenced.target);
                }
                else if (objToFix instanceof Collection)
                {   // Add element (since it was not indexable, add it to collection)
                    Collection col = (Collection) objToFix;
                    col.add(objReferenced.target);
                }
                else
                {
                    Array.set(objToFix, ref.index, objReferenced.target);        // patch array element here
                }
            }
            else
            {    // Fix field forward reference
                Field field = getDeclaredField(objToFix.getClass(), ref.field);
                if (field != null)
                {
                    try
                    {
                        field.set(objToFix, objReferenced.target);               // patch field here
                    }
                    catch (Exception e)
                    {
                        throw new IOException("Error setting field while resolving references '" + field.getName() + "', @ref = " + ref.refId);
                    }
                }
            }

            i.remove();
        }

        int count = _unresolvedRefs.size();
        if (count > 0)
        {
            StringBuilder out = new StringBuilder();
            out.append(count);
            out.append(" unresolved references:\n");
            i = _unresolvedRefs.iterator();
            count = 1;

            while (i.hasNext())
            {
                UnresolvedReference ref = (UnresolvedReference) i.next();
                out.append("    Unresolved reference ");
                out.append(count);
                out.append('\n');
                out.append("        @ref ");
                out.append(ref.refId);
                out.append('\n');
                out.append("        field ");
                out.append(ref.field);
                out.append("\n\n");
                count++;
            }
            throw new IOException(out.toString());
        }

        // Process Maps/Sets (fix up their internal indexing structure)
        // This is required because Maps hash items using hashCode(), which will
        // change between VMs.  Rehashing the map fixes this.
        i = _maps.iterator();

        while (i.hasNext())
        {
            Map map = (Map) i.next();
            Collection<Map.Entry> entries = new ArrayList<Map.Entry>(map.entrySet());
            map.clear();    // reset Map

            // Re-hash all the Map's key/value pairs.
            for (Map.Entry entry : entries)
            {
                map.put(entry.getKey(), entry.getValue());
            }
        }

        for (Object[] mapPieces : _prettyMaps)
        {
            Map map = (Map) mapPieces[0];
            Collection javaKeys = (Collection) mapPieces[1];
            Collection javaValues = (Collection) mapPieces[2];

            Iterator x = javaKeys.iterator();
            Iterator y = javaValues.iterator();

            while (x.hasNext())
            {
                map.put(x.next(), y.next());
            }
        }
    }

    public int read(char[] cbuf, int off, int len) throws IOException
    {
        throw new IOException("read(char[] _buf, int offset, int len) is not supported");
    }

    /**
     * This is a performance optimization.  The lowest 128 characters are re-used.
     *
     * @param c char to match to a Character.
     * @return a Character that matches the passed in char.  If the valuye is
     *         less than 127, then the same Character instances are re-used.
     */
    private static Character valueOf(char c)
    {
        return c <= 127 ? _charCache[(int) c] : c;
    }

    public static final int MAX_CODE_POINT = 0x10ffff;
    public static final int MIN_SUPPLEMENTARY_CODE_POINT = 0x010000;
    public static final char MIN_LOW_SURROGATE = '\uDC00';
    public static final char MIN_HIGH_SURROGATE = '\uD800';

    private static char[] toChars(int codePoint)
    {
        if (codePoint < 0 || codePoint > MAX_CODE_POINT)
        {    // int UTF-8 char must be in range
            throw new IllegalArgumentException();
        }

        if (codePoint < MIN_SUPPLEMENTARY_CODE_POINT)
        {    // if the int character fits in two bytes...
            return new char[]{(char) codePoint};
        }

        char[] result = new char[2];
        int offset = codePoint - MIN_SUPPLEMENTARY_CODE_POINT;
        result[1] = (char) ((offset & 0x3ff) + MIN_LOW_SURROGATE);
        result[0] = (char) ((offset >>> 10) + MIN_HIGH_SURROGATE);
        return result;
    }

    /**
     * This class adds significant performance increase over using the JDK
     * PushbackReader.  This is due to this class not using syncrhonization
     * as it is not needed.
     */
    private static class FastPushbackReader extends FilterReader
    {
        private int[] _buf;
        private int _idx;
        private long _pos;

        private FastPushbackReader(Reader reader, int size)
        {
            super(reader);
            if (size <= 0)
            {
                throw new IllegalArgumentException("size <= 0");
            }
            _buf = new int[size];
            _idx = size;
        }

        private FastPushbackReader(Reader r)
        {
            this(r, 1);
        }

        public long getPos()
        {
            return _pos;
        }

        public int read() throws IOException
        {
            _pos++;
            if (_idx < _buf.length)
            {
                return _buf[_idx++];
            }
            else
            {
                return super.read();
            }
        }

        public void unread(int c) throws IOException
        {
            if (_idx == 0)
            {
                throw new IOException("unread(int c) called more than pushback buffer size (" + _buf.length + "), position = " + _pos);
            }
            _pos--;
            _buf[--_idx] = c;
        }

        /**
         * Closes the stream and releases any system resources associated with
         * it. Once the stream has been closed, further read(),
         * unread(), ready(), or skip() invocations will throw an IOException.
         * Closing a previously closed stream has no effect.
         *
         * @throws java.io.IOException If an I/O error occurs
         */
        public void close() throws IOException
        {
            super.close();
            _buf = null;
            _pos = 0;
        }
    }
}
