package org.gaffo.jorb;

import org.junit.Test;

import static org.junit.Assert.assertArrayEquals;
import static org.junit.Assert.assertEquals;

public class JSONSerializerTest
{
    class Outer
    {
        String string = null;
        String [] strValues = null;
        int [] nValues = null;
    }

    @Test
    public void testRoundtrip()
    {
        Outer expected = new Outer();
        expected.string = "String";
        expected.strValues = new String[]{"one", "two", "three"};
        expected.nValues = new int[]{1, 2, 3};

        JSONSerializer out = new JSONSerializer();

        Outer actual = out.fromJson(out.toJson(expected));

        assertEquals(expected.string, actual.string);
        assertArrayEquals(expected.strValues, actual.strValues);
        assertArrayEquals(expected.nValues, actual.nValues);
    }
}
